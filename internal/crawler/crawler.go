package crawler

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/playwright-community/playwright-go"
	"go.uber.org/zap"

	"abp-bot-tiktok/internal/fetcher"
	"abp-bot-tiktok/internal/landing"
	"abp-bot-tiktok/internal/utils"
	"abp-bot-tiktok/pkg/config"
)

//nolint:unused
const (
	tiktokURL = "https://www.tiktok.com"
	searchAPI = "/api/search/item/full/"
	oneMonth  = 15 * 24 * 60 * 60
)

// BrowserConnectFunc connects to a GoLogin-managed Orbita browser via CDP.
// Injected so that tests can substitute a stub without a real browser.
type BrowserConnectFunc func(wsUrl string, options ...playwright.BrowserTypeConnectOverCDPOptions) (playwright.Browser, error)

// Crawler orchestrates the claim-loop → Playwright → ContentCrawler/ProfileCrawler → LandingWriter pipeline.
type Crawler struct {
	claimLoop      ClaimLoopIface
	connectBrowser BrowserConnectFunc
	writer         WriterIface
	contentCrawler ContentCrawlerIface
	profileCrawler ProfileCrawlerIface
	pool           *pgxpool.Pool
	cfg            *config.Config
	log            *zap.Logger
}

// New constructs a Crawler for production use (real Playwright).
func New(
	claimLoop ClaimLoopIface,
	pw *playwright.Playwright,
	writer WriterIface,
	pool *pgxpool.Pool,
	cfg *config.Config,
	log *zap.Logger,
) *Crawler {
	contentCrawler := NewTikTokUserContentCrawler(cfg.TikTokContentPageCap, log)
	profileCrawler := NewTikTokProfileCrawler(log)
	return &Crawler{
		claimLoop:      claimLoop,
		connectBrowser: pw.Chromium.ConnectOverCDP,
		writer:         writer,
		contentCrawler: contentCrawler,
		profileCrawler: profileCrawler,
		pool:           pool,
		cfg:            cfg,
		log:            log,
	}
}

// newWithDeps constructs a Crawler with all dependencies injected (used by tests).
func newWithDeps(
	claimLoop ClaimLoopIface,
	connectBrowser BrowserConnectFunc,
	writer WriterIface,
	contentCrawler ContentCrawlerIface,
	profileCrawler ProfileCrawlerIface,
	pool *pgxpool.Pool,
	cfg *config.Config,
	log *zap.Logger,
) *Crawler {
	return &Crawler{
		claimLoop:      claimLoop,
		connectBrowser: connectBrowser,
		writer:         writer,
		contentCrawler: contentCrawler,
		profileCrawler: profileCrawler,
		pool:           pool,
		cfg:            cfg,
		log:            log,
	}
}

// Handle processes a batch of claimed fetch requests. Called by ClaimLoop.Run()
// as the dispatch function.
func (c *Crawler) Handle(rows []fetcher.FetchRequest) {
	if len(rows) == 0 {
		return
	}
	ctx := context.Background()

	wsUrl := c.claimLoop.CurrentWsURL()
	var browser playwright.Browser
	if c.connectBrowser != nil && wsUrl != "" {
		var err error
		browser, err = c.connectBrowser(wsUrl) //nolint:staticcheck
		if err != nil {
			c.log.Error("connect browser", zap.String("wsUrl", wsUrl), zap.Error(err))
			errStr := fmt.Sprintf("browser connect: %s", err)
			for _, row := range rows {
				id, parseErr := uuid.Parse(row.ID)
				if parseErr != nil {
					c.log.Error("parse row id", zap.String("id", row.ID), zap.Error(parseErr))
					continue
				}
				_ = c.writer.Finalize(ctx, id, "failed", &errStr, 1)
			}
			return
		}
		defer browser.Close()
	}

	for _, row := range rows {
		c.processRow(ctx, browser, row)
	}
}

// processRow handles one fetch_request row end-to-end:
// resolve handle → open page → run content crawler → land items → finalize.
func (c *Crawler) processRow(ctx context.Context, browser playwright.Browser, row fetcher.FetchRequest) {
	id, err := uuid.Parse(row.ID)
	if err != nil {
		c.log.Error("invalid row id", zap.String("id", row.ID), zap.Error(err))
		return
	}

	// Resolve handle from platform_user_id.
	handle, err := c.resolveHandle(ctx, row.Target)
	if err != nil {
		c.log.Warn("resolve handle failed", zap.String("id", row.ID), zap.Error(err))
		errStr := "handle_unknown"
		_ = c.writer.Finalize(ctx, id, "failed", &errStr, 1)
		return
	}

	// Open a fresh page per row.
	var page playwright.Page
	if browser != nil {
		p, pageErr := browser.NewPage()
		if pageErr != nil {
			c.log.Error("new page", zap.String("id", row.ID), zap.Error(pageErr))
			errStr := fmt.Sprintf("new page: %s", pageErr)
			_ = c.writer.Finalize(ctx, id, "failed", &errStr, 1)
			return
		}
		defer p.Close()
		page = p
	}

	switch row.Scope {
	case "content":
		c.handleContent(ctx, id, page, handle, row)
	case "profile":
		c.handleProfile(ctx, id, page, handle, row)
	default:
		c.log.Warn("unknown scope", zap.String("scope", row.Scope), zap.String("id", row.ID))
		errStr := fmt.Sprintf("unknown scope: %s", row.Scope)
		_ = c.writer.Finalize(ctx, id, "failed", &errStr, 1)
	}
}

// handleContent runs TikTokUserContentCrawler and lands results.
func (c *Crawler) handleContent(ctx context.Context, id uuid.UUID, page playwright.Page, handle string, row fetcher.FetchRequest) {
	items, pagesCompleted, crawlErr := c.contentCrawler.Crawl(ctx, page, handle)

	if crawlErr != nil && len(items) == 0 {
		// Full failure — nothing was scraped.
		errStr := crawlErr.Error()
		_ = c.writer.Finalize(ctx, id, "failed", &errStr, 1)
		return
	}

	// Land each item — count successful landings.
	landedCount := 0
	for _, item := range items {
		li := landing.LandingItem{
			SourceID:       c.cfg.SourceID,
			Platform:       "tiktok",
			EntityKind:     "content",
			SourceRecordID: item.SourceRecordID,
			RawBytes:       item.RawBytes,
			FetchedAt:      item.FetchedAt,
			Envelope:       buildEnvelope(row.Target, row.Scope),
		}
		if err := c.writer.Land(ctx, li); err != nil {
			c.log.Warn("land item failed",
				zap.String("id", row.ID),
				zap.String("record_id", item.SourceRecordID),
				zap.Error(err),
			)
			continue
		}
		landedCount++
	}

	if landedCount == 0 {
		// Nothing successfully landed.
		var errStr string
		if crawlErr != nil {
			errStr = crawlErr.Error()
		} else {
			errStr = "all items failed to land"
		}
		_ = c.writer.Finalize(ctx, id, "failed", &errStr, 1)
		return
	}

	if crawlErr != nil {
		// Partial success — some items landed but pagination failed.
		errStr := fmt.Sprintf("partial: landed %d items across %d pages, failed on page %d: %s",
			landedCount, pagesCompleted, pagesCompleted+1, crawlErr)
		_ = c.writer.Finalize(ctx, id, "landed", &errStr, 0)
		return
	}

	// Full success.
	_ = c.writer.Finalize(ctx, id, "landed", nil, 0)
}

// handleProfile runs TikTokProfileCrawler and lands the profile record.
func (c *Crawler) handleProfile(ctx context.Context, id uuid.UUID, page playwright.Page, handle string, row fetcher.FetchRequest) {
	item, err := c.profileCrawler.Crawl(ctx, page, handle)
	if err != nil {
		errStr := err.Error()
		_ = c.writer.Finalize(ctx, id, "failed", &errStr, 1)
		return
	}

	li := landing.LandingItem{
		SourceID:       c.cfg.SourceID,
		Platform:       "tiktok",
		EntityKind:     "profile",
		SourceRecordID: item.SourceRecordID,
		RawBytes:       item.RawBytes,
		FetchedAt:      item.FetchedAt,
		Envelope:       buildEnvelope(row.Target, row.Scope),
	}
	if err := c.writer.Land(ctx, li); err != nil {
		c.log.Warn("land profile failed", zap.String("id", row.ID), zap.Error(err))
		errStr := err.Error()
		_ = c.writer.Finalize(ctx, id, "failed", &errStr, 1)
		return
	}

	_ = c.writer.Finalize(ctx, id, "landed", nil, 0)
}

// resolveHandle queries canonical.social_account for the TikTok handle.
func (c *Crawler) resolveHandle(ctx context.Context, target string) (string, error) {
	var handle *string
	err := c.pool.QueryRow(ctx, `
		SELECT handle
		FROM canonical.social_account
		WHERE platform = 'tiktok' AND platform_user_id = $1
	`, target).Scan(&handle)
	if err != nil {
		return "", fmt.Errorf("query handle for %s: %w", target, err)
	}
	if handle == nil || *handle == "" {
		return "", fmt.Errorf("null handle for target %s", target)
	}
	return *handle, nil
}

// buildEnvelope constructs the FetchEnvelope-compatible JSON object.
func buildEnvelope(target, scope string) map[string]any {
	return map[string]any{
		"status": "ok",
		"request": map[string]any{
			"target": target,
			"scope":  scope,
		},
		"provenance": map[string]any{
			"source_nature": "scraper",
			"confidence":    0.7,
		},
		"rate_limit": nil,
		"error":      nil,
	}
}

// ---- dormant crawlKeyword (keyword-search path, not wired in F6) ----

//nolint:unused
func (c *Crawler) crawlKeyword(page playwright.Page, keyword string, log *zap.Logger, tag string) {
	startTime := time.Now()

	//nolint:errcheck
	page.Route("**/*", func(route playwright.Route) {
		switch route.Request().ResourceType() {
		case "stylesheet", "font", "image", "media", "other":
			_ = route.Abort()
		default:
			_ = route.Continue()
		}
	})

	var mu sync.Mutex
	var collectedItems []map[string]any

	page.On("response", func(res playwright.Response) {
		if !containsAny(res.URL(), []string{searchAPI}) {
			return
		}
		go func(res playwright.Response) {
			var body map[string]any
			if err := res.JSON(&body); err != nil || body == nil {
				log.Sugar().Warnf("%s   API response parse error: %v | url: %s", tag, err, res.URL())
				return
			}
			statusCode, _ := body["status_code"].(float64)
			items, _ := body["item_list"].([]any)
			log.Sugar().Infof("%s   API hit: status=%v items=%d", tag, statusCode, len(items))

			if statusCode == 2061 || statusCode == 10000 || statusCode == -1 {
				log.Sugar().Warnf("%s   ⚠️ Rate limited (status=%v), sleeping 5 minutes...", tag, statusCode)
				time.Sleep(5 * time.Minute)
				return
			}

			if statusCode != 0 {
				return
			}

			mu.Lock()
			defer mu.Unlock()
			seen := make(map[string]bool)
			for _, existing := range collectedItems {
				if id, ok := existing["id"].(string); ok {
					seen[id] = true
				}
			}
			for _, raw := range items {
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

	if _, err := page.Goto(tiktokURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	}); err != nil {
		log.Sugar().Warnf("%s TikTok home failed: %v", tag, err)
		return
	}
	utils.Sleep(4000, 7000)
	_ = utils.RandomMouseMove(page)
	utils.Sleep(500, 1500)

	encoded := url.QueryEscape(keyword)
	ts := time.Now().UnixMilli()

	topURL := fmt.Sprintf("%s/search?q=%s&t=%d", tiktokURL, encoded, ts)
	if _, err := page.Goto(topURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	}); err != nil {
		log.Sugar().Warnf("%s Search navigate failed: %v", tag, err)
		return
	}
	utils.Sleep(3000, 5000)

	hasVideos, _ := page.Evaluate(`() => {
		const items = document.querySelectorAll('[data-e2e="search-common-video"], [class*="DivItemContainer"]');
		return items.length > 0;
	}`)
	if hasVideos == nil || hasVideos == false {
		log.Sugar().Infof("%s   Top tab empty, switching to Video tab...", tag)
		videoURL := fmt.Sprintf("%s/search/video?q=%s&t=%d", tiktokURL, encoded, ts)
		if _, err := page.Goto(videoURL, playwright.PageGotoOptions{
			WaitUntil: playwright.WaitUntilStateDomcontentloaded,
			Timeout:   playwright.Float(30000),
		}); err != nil {
			log.Sugar().Warnf("%s Video tab navigate failed: %v", tag, err)
			return
		}
		utils.Sleep(4000, 6000)
	}

	scrollTimes := utils.RandInt(10, 15)
	log.Sugar().Infof("%s   Scrolling %d times to load videos...", tag, scrollTimes)
	_ = utils.HumanScroll(page, scrollTimes)

	_ = utils.RandomViewVideo(page)
	utils.Sleep(1500, 2500)

	mu.Lock()
	items := collectedItems
	mu.Unlock()

	_ = parseVideos(keyword, items)

	duration := time.Since(startTime)
	log.Sugar().Infof("%s   %q -> %d videos | ⏱️ %s", tag, keyword, len(collectedItems), duration.Round(time.Second))
}

//nolint:unused
func parseVideos(keyword string, items []map[string]any) []map[string]any {
	nowTs := time.Now().Unix()
	cutoff := nowTs - oneMonth
	var results []map[string]any

	for _, item := range items {
		pubTime := int64(toFloat(item["createTime"]))
		if pubTime < cutoff {
			continue
		}
		results = append(results, item)
	}
	return results
}

//nolint:unused
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
