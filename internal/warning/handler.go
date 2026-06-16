package warning

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"abp-bot-tiktok/internal/models"
	"abp-bot-tiktok/internal/parser"
	"abp-bot-tiktok/internal/utils"
	"abp-bot-tiktok/pkg/config"
	"abp-bot-tiktok/pkg/gpm"
	kafkapkg "abp-bot-tiktok/pkg/kafka"

	"github.com/playwright-community/playwright-go"
	"go.uber.org/zap"
)

const (
	oneMonth      = 90 * 24 * 60 * 60
	profileAPI    = "/api/post/item_list/"
	searchAPI     = "/api/search/item/full/"
	videoDetailAPI = "/api/item/detail/"
	outputTopic   = "abp-manual-message-orchestrator"
)

// Message matches the PostEntity payload published to manual.warnings.{source}.
type Message struct {
	ID              string  `json:"id"`
	DocType         int     `json:"doc_type"`
	SourceType      int     `json:"source_type"`
	CrawlSource     int     `json:"crawl_source"`
	CrawlSourceCode string  `json:"crawl_source_code"`
	PubTime         int64   `json:"pub_time"`
	CrawlTime       int64   `json:"crawl_time"`
	OrgID           int     `json:"org_id"`
	OrgIDAlias      string  `json:"orgId"`
	IsAlert         bool    `json:"isAlert"`
	SubjectID       string  `json:"subject_id"`
	Title           string  `json:"title"`
	Description     string  `json:"description"`
	Content         string  `json:"content"`
	URL             string  `json:"url"`
	MediaURLs       string  `json:"media_urls"`
	Comments        int64   `json:"comments"`
	Shares          int64   `json:"shares"`
	Reactions       int64   `json:"reactions"`
	Favors          int64   `json:"favors"`
	Views           int64   `json:"views"`
	WebTags         string  `json:"web_tags"`
	WebKeywords     string  `json:"web_keywords"`
	AuthID          string  `json:"auth_id"`
	AuthName        string  `json:"auth_name"`
	AuthType        int     `json:"auth_type"`
	AuthURL         string  `json:"auth_url"`
	SourceID        string  `json:"source_id"`
	SourceName      string  `json:"source_name"`
	SourceURL       string  `json:"source_url"`
	ReplyTo         *string `json:"reply_to"`
	Level           int     `json:"level"`
	Sentiment       int     `json:"sentiment"`
	IsPriority      bool    `json:"isPriority"`
	CrawlBot     string     `json:"crawl_bot"`
	Link         string     `json:"link"`
	Source       string     `json:"source"`
	Status       string     `json:"status"`
	ErrorMessage *string    `json:"error_message,omitempty"`
	CrawledAt    *time.Time `json:"crawledAt,omitempty"`
}

type Handler struct {
	cfg      *config.Config
	log      *zap.Logger
	pw       *playwright.Playwright
	producer *kafkapkg.Producer
}

func NewHandler(cfg *config.Config, log *zap.Logger) (*Handler, error) {
	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("playwright start: %w", err)
	}
	producer := kafkapkg.NewProducer(cfg.KafkaBrokers, outputTopic, log)
	return &Handler{cfg: cfg, log: log, pw: pw, producer: producer}, nil
}

func (h *Handler) Close() {
	h.pw.Stop()
	h.producer.Close()
}

func (h *Handler) Handle(data []byte) error {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		h.log.Sugar().Warnf("[warning] json parse error: %v", err)
		if msg.Link == "" {
			msg.Link = string(data)
		}
	}

	// orgId có thể là string "50" → parse sang int
	if msg.OrgID == 0 && msg.OrgIDAlias != "" {
		if n, err := strconv.Atoi(msg.OrgIDAlias); err == nil {
			msg.OrgID = n
		}
	}

	h.log.Sugar().Infof("[warning] received | link=%s source=%s org_id=%d isAlert=%v",
		msg.Link, msg.Source, msg.OrgID, msg.IsAlert)

	if msg.Link == "" {
		h.log.Warn("[warning] empty link in message, skipping")
		return nil
	}

	profileID := h.cfg.WarningProfileID
	if profileID == "" && len(h.cfg.ProfileIDs) > 0 {
		profileID = h.cfg.ProfileIDs[0]
	}
	if profileID == "" {
		return h.publishFailed(msg, "no GPM profile ID available")
	}

	h.log.Sugar().Infof("[warning] id=%s source=%s org_id=%d auth=%s link=%s",
		msg.ID, msg.Source, msg.OrgID, msg.AuthName, msg.Link)

	if err := h.gotoURL(profileID, msg); err != nil {
		return h.publishFailed(msg, err.Error())
	}
	return nil
}

// gotoURL opens GPM, navigates, scrolls to collect videos, then produces to Kafka.
func (h *Handler) gotoURL(profileID string, msg Message) error {
	gpmClient := gpm.NewClient(h.cfg.GPMAPI, h.log)

	wsURL, err := gpmClient.StartProfile(profileID)
	if err != nil {
		return fmt.Errorf("GPM start: %w", err)
	}

	browser, err := h.pw.Chromium.ConnectOverCDP(wsURL)
	if err != nil {
		return fmt.Errorf("CDP connect: %w", err)
	}

	contexts := browser.Contexts()
	if len(contexts) == 0 {
		return fmt.Errorf("no browser context from GPM")
	}

	page, err := contexts[0].NewPage()
	if err != nil {
		return fmt.Errorf("new page: %w", err)
	}

	// Intercept TikTok API responses to collect video items
	var mu sync.Mutex
	var collectedItems []map[string]any

	page.On("response", func(res playwright.Response) {
		url := res.URL()
		if !containsAny(url, []string{profileAPI, searchAPI, videoDetailAPI}) {
			return
		}
		go func(res playwright.Response) {
			var body map[string]any
			if err := res.JSON(&body); err != nil || body == nil {
				return
			}

			var rawItems []any

			switch {
			// Single video detail: /api/item/detail/
			case containsAny(url, []string{videoDetailAPI}):
				if info, ok := body["itemInfo"].(map[string]any); ok {
					if item, ok := info["itemStruct"].(map[string]any); ok {
						rawItems = []any{item}
					}
				}
			// Profile list: /api/post/item_list/
			case containsAny(url, []string{profileAPI}):
				if v, ok := body["itemList"].([]any); ok {
					rawItems = v
				}
			// Search: /api/search/item/full/
			default:
				if v, ok := body["item_list"].([]any); ok {
					rawItems = v
				}
			}

			if len(rawItems) == 0 {
				return
			}
			h.log.Sugar().Infof("[warning] API hit %s items=%d", url, len(rawItems))

			mu.Lock()
			defer mu.Unlock()
			seen := make(map[string]bool)
			for _, existing := range collectedItems {
				if id, ok := existing["id"].(string); ok {
					seen[id] = true
				}
			}
			for _, raw := range rawItems {
				item, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				id, _ := item["id"].(string)
				if id == "" || seen[id] {
					continue
				}
				seen[id] = true
				collectedItems = append(collectedItems, item)
			}
		}(res)
	})

	if _, err := page.Goto(msg.Link, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	}); err != nil {
		return fmt.Errorf("goto timeout/blocked: %w", err)
	}
	h.log.Sugar().Infof("[warning] navigated to %s", msg.Link)
	utils.Sleep(3000, 5000)

	mu.Lock()
	items := collectedItems
	mu.Unlock()

	h.log.Sugar().Infof("[warning] collected %d items", len(items))

	posts := h.parseItems(items, msg.OrgID)

	h.log.Sugar().Infof("[warning] ── crawled %d posts ──────────────────────", len(posts))
	for i, post := range posts {
		pubTime := time.Unix(post.PubTime, 0).Format("2006-01-02 15:04")
		desc := post.Description
		if len(desc) > 80 {
			desc = desc[:80] + "..."
		}
		h.log.Sugar().Infof("[warning] [%d/%d] id=%-20s org=%-6d auth=%-20s views=%-8d comments=%-6d pub=%s",
			i+1, len(posts), post.SubjectID, post.OrgID, post.AuthName, post.Views, post.Comments, pubTime)
		h.log.Sugar().Infof("[warning]        url=%s", post.URL)
		h.log.Sugar().Infof("[warning]        desc=%s", desc)
	}
	h.log.Sugar().Infof("[warning] ─────────────────────────────────────────────")

	ctx := context.Background()
	for _, post := range posts {
		if err := h.producer.Publish(ctx, post.SubjectID, post); err != nil {
			h.log.Sugar().Errorf("[warning] produce failed for %s: %v", post.SubjectID, err)
		}
	}
	h.log.Sugar().Infof("[warning] produced %d posts to %s", len(posts), outputTopic)

	return nil
}

// publishFailed produces the original message back with status=FAILED.
func (h *Handler) publishFailed(msg Message, reason string) error {
	msg.Status = "FAILED"
	msg.ErrorMessage = &reason
	h.log.Sugar().Errorf("[warning] FAILED id=%s reason=%s", msg.ID, reason)
	return h.producer.Publish(context.Background(), msg.ID, msg)
}

func (h *Handler) parseItems(items []map[string]any, orgID int) []parser.TiktokPost {
	nowTs := time.Now().Unix()
	cutoff := nowTs - oneMonth
	var posts []parser.TiktokPost

	for _, item := range items {
		pubTime := int64(toFloat(item["createTime"]))
		if pubTime > 0 && pubTime < cutoff {
			continue
		}
		author, _ := item["author"].(map[string]any)
		stats, _ := item["stats"].(map[string]any)

		v := models.VideoItem{
			OrgID:       orgID,
			VideoID:     toString(item["id"]),
			Description: toString(item["desc"]),
			PubTime:     pubTime,
			UniqueID:    toString(mapGet(author, "uniqueId")),
			AuthID:      toString(mapGet(author, "id")),
			AuthName:    toString(mapGet(author, "nickname")),
			Comments:    int64(toFloat(mapGet(stats, "commentCount"))),
			Shares:      int64(toFloat(mapGet(stats, "shareCount"))),
			Reactions:   int64(toFloat(mapGet(stats, "diggCount"))),
			Favors:      int64(toFloat(mapGet(stats, "collectCount"))),
			Views:       int64(toFloat(mapGet(stats, "playCount"))),
		}
		posts = append(posts, parser.FromVideoItem(v))
	}
	return posts
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func toFloat(v any) float64 {
	if v == nil {
		return 0
	}
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

func mapGet(m map[string]any, key string) any {
	if m == nil {
		return nil
	}
	return m[key]
}
