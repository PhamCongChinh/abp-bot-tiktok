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
	apipkg "abp-bot-tiktok/pkg/api"
	"abp-bot-tiktok/pkg/config"
	"abp-bot-tiktok/pkg/gpm"
	kafkapkg "abp-bot-tiktok/pkg/kafka"

	"github.com/playwright-community/playwright-go"
	"go.uber.org/zap"
)

const (
	oneMonth       = 90 * 24 * 60 * 60
	profileAPI     = "/api/post/item_list/"
	searchAPI      = "/api/search/item/full/"
	videoDetailAPI = "/api/item/detail/"
	outputTopic    = "abp-manual-message-orchestrator"
	maxAttempts    = 5
)

type Message struct {
	ID              string     `json:"id"`
	DocType         int        `json:"doc_type"`
	SourceType      int        `json:"source_type"`
	CrawlSource     int        `json:"crawl_source"`
	CrawlSourceCode string     `json:"crawl_source_code"`
	PubTime         int64      `json:"pub_time"`
	CrawlTime       int64      `json:"crawl_time"`
	OrgID           int        `json:"org_id"`
	OrgIDAlias      string     `json:"orgId"`
	IsAlert         bool       `json:"isAlert"`
	SubjectID       string     `json:"subject_id"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	Content         string     `json:"content"`
	URL             string     `json:"url"`
	MediaURLs       string     `json:"media_urls"`
	Comments        int64      `json:"comments"`
	Shares          int64      `json:"shares"`
	Reactions       int64      `json:"reactions"`
	Favors          int64      `json:"favors"`
	Views           int64      `json:"views"`
	WebTags         string     `json:"web_tags"`
	WebKeywords     string     `json:"web_keywords"`
	AuthID          string     `json:"auth_id"`
	AuthName        string     `json:"auth_name"`
	AuthType        int        `json:"auth_type"`
	AuthURL         string     `json:"auth_url"`
	SourceID        string     `json:"source_id"`
	SourceName      string     `json:"source_name"`
	SourceURL       string     `json:"source_url"`
	ReplyTo         *string    `json:"reply_to"`
	Level           int        `json:"level"`
	Sentiment       int        `json:"sentiment"`
	IsPriority      bool       `json:"isPriority"`
	CrawlBot        string     `json:"crawl_bot"`
	Link            string     `json:"link"`
	Source          string     `json:"source"`
	Status          string     `json:"status"`
	ErrorMessage    *string    `json:"error_message,omitempty"`
	CrawledAt       *time.Time `json:"crawledAt,omitempty"`
}

// profilePool quản lý GPM profile IDs theo round-robin, thread-safe.
type profilePool struct {
	mu     sync.Mutex
	ids    []string
	busy   map[string]bool
	cursor int
}

func newProfilePool(ids []string) *profilePool {
	return &profilePool{ids: ids, busy: make(map[string]bool)}
}

// acquireNext trả về profile ID tiếp theo không bận (round-robin).
// Trả về "" nếu tất cả đang bận.
func (p *profilePool) acquireNext() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.ids)
	if n == 0 {
		return ""
	}
	for i := 0; i < n; i++ {
		idx := (p.cursor + i) % n
		id := p.ids[idx]
		if !p.busy[id] {
			p.busy[id] = true
			p.cursor = (idx + 1) % n
			return id
		}
	}
	return ""
}

// waitAcquire chờ tối đa timeout để lấy profile rảnh.
func (p *profilePool) waitAcquire(timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if id := p.acquireNext(); id != "" {
			return id, true
		}
		time.Sleep(3 * time.Second)
	}
	return "", false
}

func (p *profilePool) release(id string) {
	if id == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.busy, id)
}

func (p *profilePool) hasProfiles() bool {
	return len(p.ids) > 0
}

type Handler struct {
	cfg       *config.Config
	log       *zap.Logger
	pw        *playwright.Playwright
	producer  *kafkapkg.Producer
	apiClient *apipkg.Client
	gpmClient *gpm.Client
	pool      *profilePool
}

func NewHandler(cfg *config.Config, log *zap.Logger) (*Handler, error) {
	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("playwright start: %w", err)
	}
	producer := kafkapkg.NewProducer(cfg.KafkaBrokers, outputTopic, log)
	var apiClient *apipkg.Client
	if cfg.APIURL != "" {
		apiClient = apipkg.NewClient(cfg.APIURL, log)
	}
	var gpmClient *gpm.Client
	if cfg.UseGPM {
		gpmClient = gpm.NewClient(cfg.GPMAPI, log)
	}
	pool := newProfilePool(cfg.ProfileIDs)
	return &Handler{
		cfg:       cfg,
		log:       log,
		pw:        pw,
		producer:  producer,
		apiClient: apiClient,
		gpmClient: gpmClient,
		pool:      pool,
	}, nil
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
	if msg.OrgID == 0 && msg.OrgIDAlias != "" {
		if n, err := strconv.Atoi(msg.OrgIDAlias); err == nil {
			msg.OrgID = n
		}
	}

	h.log.Sugar().Infof("[warning] received | link=%s source=%s org_id=%d isAlert=%v",
		msg.Link, msg.Source, msg.OrgID, msg.IsAlert)

	if msg.Link == "" {
		h.log.Warn("[warning] skipping: empty link")
		return nil
	}
	if msg.OrgID == 0 {
		h.log.Warn("[warning] skipping: org_id = 0")
		return nil
	}

	ctx := context.Background()
	useGPM := h.gpmClient != nil && h.pool.hasProfiles()

	var lastErr string
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var profileID string

		if useGPM {
			var ok bool
			profileID, ok = h.pool.waitAcquire(60 * time.Second)
			if !ok {
				lastErr = "no profile available after 60s"
				h.log.Sugar().Errorf("[warning] attempt %d/%d: %s", attempt, maxAttempts, lastErr)
				continue
			}
			h.log.Sugar().Infof("[warning] attempt %d/%d profile=%.8s...", attempt, maxAttempts, profileID)
		} else {
			h.log.Sugar().Infof("[warning] attempt %d/%d (direct Chrome)", attempt, maxAttempts)
		}

		posts, err := h.crawl(msg, profileID)

		if useGPM {
			h.pool.release(profileID)
		}

		if err != nil {
			lastErr = err.Error()
			h.log.Sugar().Warnf("[warning] attempt %d/%d failed: %v", attempt, maxAttempts, err)
			continue
		}
		if len(posts) == 0 {
			lastErr = "cannot extract video data from SSR or API intercept"
			h.log.Sugar().Warnf("[warning] attempt %d/%d no data found", attempt, maxAttempts)
			continue
		}

		// SUCCESS
		h.logPosts(posts)

		for _, post := range posts {
			wp := apipkg.FromTiktokPost(post, msg.Link, msg.Source)
			if err := h.producer.Publish(ctx, post.SubjectID, wp); err != nil {
				h.log.Sugar().Errorf("[warning] kafka publish failed: %v", err)
			}
		}
		h.log.Sugar().Infof("[warning] published %d posts to kafka %s", len(posts), outputTopic)

		if h.apiClient != nil {
			if err := h.apiClient.PostWarning(posts, msg.Link, msg.Source); err != nil {
				h.log.Sugar().Errorf("[warning] push to API failed: %v", err)
			} else {
				h.log.Sugar().Infof("[warning] pushed %d posts to /api/v1/posts/insert-posts", len(posts))
			}
		}
		return nil
	}

	// Tất cả attempts thất bại
	h.log.Sugar().Errorf("[warning] FAILED after %d attempts: %s", maxAttempts, lastErr)
	failed := apipkg.FailedWarningPost(msg.Link, msg.Source, msg.OrgID, lastErr)
	_ = h.producer.Publish(ctx, msg.ID, failed)
	return nil
}

// crawl mở browser (GPM hoặc direct Chrome), navigate, extract dữ liệu.
// profileID="" → direct Chrome.
func (h *Handler) crawl(msg Message, profileID string) ([]parser.TiktokPost, error) {
	var page playwright.Page

	if profileID != "" {
		// GPM mode: kết nối qua CDP
		wsURL, err := h.gpmClient.StartProfile(profileID)
		if err != nil {
			return nil, fmt.Errorf("GPM start profile %.8s: %w", profileID, err)
		}
		browser, err := h.pw.Chromium.ConnectOverCDP(wsURL)
		if err != nil {
			return nil, fmt.Errorf("CDP connect profile %.8s: %w", profileID, err)
		}
		defer browser.Close()

		contexts := browser.Contexts()
		if len(contexts) == 0 {
			return nil, fmt.Errorf("no browser context from GPM profile %.8s", profileID)
		}
		p, err := contexts[0].NewPage()
		if err != nil {
			return nil, fmt.Errorf("new page profile %.8s: %w", profileID, err)
		}
		page = p
	} else {
		// Direct Chrome
		chromePath := `C:\Program Files\Google\Chrome\Application\chrome.exe`
		browser, err := h.pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
			Headless:       playwright.Bool(false),
			ExecutablePath: playwright.String(chromePath),
		})
		if err != nil {
			return nil, fmt.Errorf("launch Chrome: %w", err)
		}
		defer browser.Close()

		p, err := browser.NewPage()
		if err != nil {
			return nil, fmt.Errorf("new page: %w", err)
		}
		page = p
	}

	// Intercept TikTok API responses
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
			case containsAny(url, []string{videoDetailAPI}):
				if info, ok := body["itemInfo"].(map[string]any); ok {
					if item, ok := info["itemStruct"].(map[string]any); ok {
						rawItems = []any{item}
					}
				}
			case containsAny(url, []string{profileAPI}):
				if v, ok := body["itemList"].([]any); ok {
					rawItems = v
				}
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

	h.log.Sugar().Info("[warning] browser opened, waiting 2s...")
	time.Sleep(2 * time.Second)

	if _, err := page.Goto(msg.Link, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	}); err != nil {
		return nil, fmt.Errorf("goto: %w", err)
	}
	h.log.Sugar().Infof("[warning] navigated to %s", msg.Link)
	utils.Sleep(3000, 5000)

	// SSR extraction
	var posts []parser.TiktokPost
	if ssrPost := h.extractSSRPost(page, msg.OrgID); ssrPost != nil {
		posts = append(posts, *ssrPost)
	} else {
		h.log.Warn("[warning] SSR not found, trying API intercept")
		utils.Sleep(2000, 3000)
		mu.Lock()
		items := collectedItems
		mu.Unlock()
		posts = h.parseItems(items, msg.OrgID)
	}

	h.log.Sugar().Info("[warning] waiting 10s before closing browser...")
	time.Sleep(10 * time.Second)
	page.Close()

	return posts, nil
}

func (h *Handler) logPosts(posts []parser.TiktokPost) {
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
	h.log.Sugar().Info("[warning] ─────────────────────────────────────────────")
}

func (h *Handler) extractSSRPost(page playwright.Page, orgID int) *parser.TiktokPost {
	raw, err := page.Evaluate(`() => {
		const el = document.getElementById('__UNIVERSAL_DATA_FOR_REHYDRATION__');
		return el ? el.textContent : null;
	}`)
	if err != nil || raw == nil {
		h.log.Sugar().Warnf("[warning] SSR script tag not found: %v", err)
		return nil
	}
	text, ok := raw.(string)
	if !ok || text == "" {
		h.log.Warn("[warning] SSR text empty")
		return nil
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		h.log.Sugar().Warnf("[warning] SSR json parse error: %v", err)
		return nil
	}
	scope, _ := data["__DEFAULT_SCOPE__"].(map[string]any)
	if scope != nil {
		keys := make([]string, 0, len(scope))
		for k := range scope {
			keys = append(keys, k)
		}
		h.log.Sugar().Infof("[warning] SSR scope keys: %v", keys)
	}
	videoDetail, _ := scope["webapp.video-detail"].(map[string]any)
	itemInfo, _ := videoDetail["itemInfo"].(map[string]any)
	item, _ := itemInfo["itemStruct"].(map[string]any)
	if item == nil {
		return nil
	}

	author, _ := item["author"].(map[string]any)
	stats, _ := item["stats"].(map[string]any)

	v := models.VideoItem{
		OrgID:       orgID,
		VideoID:     toString(item["id"]),
		Description: toString(item["desc"]),
		PubTime:     int64(toFloat(item["createTime"])),
		UniqueID:    toString(mapGet(author, "uniqueId")),
		AuthID:      toString(mapGet(author, "id")),
		AuthName:    toString(mapGet(author, "nickname")),
		Comments:    int64(toFloat(mapGet(stats, "commentCount"))),
		Shares:      int64(toFloat(mapGet(stats, "shareCount"))),
		Reactions:   int64(toFloat(mapGet(stats, "diggCount"))),
		Favors:      int64(toFloat(mapGet(stats, "collectCount"))),
		Views:       int64(toFloat(mapGet(stats, "playCount"))),
	}
	post := parser.FromVideoItem(v)
	return &post
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
	if s, ok := v.(string); ok {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	return 0
}

func mapGet(m map[string]any, key string) any {
	if m == nil {
		return nil
	}
	return m[key]
}
