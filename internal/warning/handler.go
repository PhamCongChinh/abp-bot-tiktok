package warning

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"abp-bot-tiktok/internal/models"
	"abp-bot-tiktok/internal/parser"
	"abp-bot-tiktok/internal/utils"
	"abp-bot-tiktok/pkg/api"
	"abp-bot-tiktok/pkg/config"
	"abp-bot-tiktok/pkg/gpm"

	"github.com/playwright-community/playwright-go"
	"go.uber.org/zap"
)

const (
	oneMonth   = 90 * 24 * 60 * 60
	profileAPI = "/api/post/item_list/"
	searchAPI  = "/api/search/item/full/"
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
	CrawlBot        string  `json:"crawl_bot"`
	Link            string  `json:"link"`
	Source          string  `json:"source"`
	Status          string  `json:"status"`
	ErrorMessage    *string `json:"error_message"`
}

type Handler struct {
	cfg       *config.Config
	log       *zap.Logger
	pw        *playwright.Playwright
	apiClient *api.Client
}

func NewHandler(cfg *config.Config, log *zap.Logger) (*Handler, error) {
	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("playwright start: %w", err)
	}
	var apiClient *api.Client
	if cfg.APIURL != "" {
		apiClient = api.NewClient(cfg.APIURL, log)
	}
	return &Handler{cfg: cfg, log: log, pw: pw, apiClient: apiClient}, nil
}

func (h *Handler) Close() {
	h.pw.Stop()
}

func (h *Handler) Handle(data []byte) error {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		msg.Link = string(data)
	}

	if msg.Link == "" {
		h.log.Warn("[warning] empty link in message, skipping")
		return nil
	}

	profileID := h.cfg.WarningProfileID
	if profileID == "" && len(h.cfg.ProfileIDs) > 0 {
		profileID = h.cfg.ProfileIDs[0]
	}
	if profileID == "" {
		return fmt.Errorf("[warning] no GPM profile ID available")
	}

	h.log.Sugar().Infof("[warning] id=%s source=%s org_id=%d auth=%s sentiment=%d priority=%v link=%s",
		msg.ID, msg.Source, msg.OrgID, msg.AuthName, msg.Sentiment, msg.IsPriority, msg.Link)
	return h.gotoURL(profileID, msg.Link, msg.OrgID)
}

// gotoURL opens GPM, navigates to targetURL, scrolls to collect videos, then pushes to API.
func (h *Handler) gotoURL(profileID, targetURL string, orgID int) error {
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
		if !containsAny(url, []string{profileAPI, searchAPI}) {
			return
		}
		go func(res playwright.Response) {
			var body map[string]any
			if err := res.JSON(&body); err != nil || body == nil {
				return
			}

			// Profile API returns "itemList", search API returns "item_list"
			var rawItems []any
			if v, ok := body["itemList"].([]any); ok {
				rawItems = v
			} else if v, ok := body["item_list"].([]any); ok {
				rawItems = v
			}

			if len(rawItems) == 0 {
				return
			}

			h.log.Sugar().Infof("[warning] API hit: %s items=%d", url, len(rawItems))

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

	if _, err := page.Goto(targetURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	}); err != nil {
		return fmt.Errorf("goto %s: %w", targetURL, err)
	}
	h.log.Sugar().Infof("[warning] navigated to %s", targetURL)

	// Scroll 10-15 times, 1-3s between each scroll
	scrollTimes := utils.RandInt(10, 15)
	h.log.Sugar().Infof("[warning] scrolling %d times...", scrollTimes)
	for i := 0; i < scrollTimes; i++ {
		page.Mouse().Move(
			float64(utils.RandInt(300, 700)),
			float64(utils.RandInt(200, 500)),
		)
		page.Mouse().Wheel(0, float64(utils.RandInt(600, 900)))
		h.log.Sugar().Infof("[warning] scroll %d/%d", i+1, scrollTimes)
		utils.Sleep(1000, 3000)
	}

	mu.Lock()
	items := collectedItems
	mu.Unlock()

	h.log.Sugar().Infof("[warning] collected %d items", len(items))

	if len(items) > 0 && h.apiClient != nil {
		posts := h.parseAndPush(items, orgID)
		if err := h.apiClient.PostUnclassified(posts); err != nil {
			h.log.Sugar().Errorf("[warning] push to API failed: %v", err)
		} else {
			h.log.Sugar().Infof("[warning] pushed %d posts to API", len(posts))
		}
	}

	return nil
}

func (h *Handler) parseAndPush(items []map[string]any, orgID int) []parser.TiktokPost {
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
