package crawler

import (
	"abp-bot-tiktok/internal/models"
	"abp-bot-tiktok/internal/parser"
	"abp-bot-tiktok/internal/repository"
	"abp-bot-tiktok/internal/utils"
	"abp-bot-tiktok/pkg/api"
	"abp-bot-tiktok/pkg/config"
	"abp-bot-tiktok/pkg/gpm"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/playwright-community/playwright-go"
	"go.uber.org/zap"
)

const (
	tiktokURL = "https://www.tiktok.com"
	searchAPI = "/api/search/item/full/"
	sevenDays = 7 * 24 * 60 * 60
)

type Crawler struct {
	cfg       *config.Config
	log       *zap.Logger
	videoRepo *repository.VideoRepository
	apiClient *api.Client
}

func New(cfg *config.Config, log *zap.Logger, videoRepo *repository.VideoRepository) *Crawler {
	var apiClient *api.Client
	if cfg.APIURL != "" {
		apiClient = api.NewClient(cfg.APIURL, log)
		log.Info("API client initialized", zap.String("url", cfg.APIURL))
	}
	return &Crawler{
		cfg:       cfg,
		log:       log,
		videoRepo: videoRepo,
		apiClient: apiClient,
	}
}

func (c *Crawler) Run() {
	c.log.Info("========================================")
	c.log.Info("Crawl cycle started")
	c.log.Info("========================================")

	if !c.cfg.UseGPM {
		c.log.Error("GPM config required. Set GPM_API and PROFILE_IDS in .env")
		return
	}

	numProfiles := len(c.cfg.ProfileIDs)
	c.log.Info("Configuration",
		zap.Int("total_profiles", numProfiles),
		zap.Int("total_keywords", len(c.cfg.Keywords)),
	)

	// Split keywords evenly across profiles
	chunks := splitKeywords(c.cfg.Keywords, numProfiles)

	c.log.Info("========================================")
	c.log.Info("Keyword distribution across profiles:")
	c.log.Info("========================================")
	for i, chunk := range chunks {
		c.log.Info("",
			zap.String("profile_id", c.cfg.ProfileIDs[i]),
			zap.Int("profile_index", i+1),
			zap.Int("keywords_count", len(chunk)),
			zap.Strings("keywords", chunk),
		)
	}
	c.log.Info("========================================")

	// Run each profile in parallel goroutine
	var wg sync.WaitGroup
	for i, profileID := range c.cfg.ProfileIDs {
		wg.Add(1)
		go func(profileID string, keywords []string, idx int) {
			defer wg.Done()
			c.runProfile(profileID, keywords, idx)
		}(profileID, chunks[i], i)
	}

	wg.Wait()
	c.log.Info("========================================")
	c.log.Info("Crawl cycle finished")
	c.log.Info("========================================")
}

func splitKeywords(keywords []string, n int) [][]string {
	chunks := make([][]string, n)
	for i, kw := range keywords {
		chunks[i%n] = append(chunks[i%n], kw)
	}
	return chunks
}

func (c *Crawler) runProfile(profileID string, keywords []string, idx int) {
	prefix := fmt.Sprintf("[profile-%d | %s]", idx+1, profileID[:8])
	log := c.log.With(zap.String("profile", prefix))

	log.Info("========================================")
	log.Info(prefix+" Profile starting", zap.Int("keywords_count", len(keywords)))
	log.Info("========================================")

	pw, err := playwright.Run()
	if err != nil {
		log.Error(prefix+" Could not start playwright", zap.Error(err))
		return
	}
	defer pw.Stop()

	gpmClient := gpm.NewClient(c.cfg.GPMAPI, log)
	browser, context, err := c.connectGPMWithRetry(pw, gpmClient, profileID, log, 3)
	if err != nil {
		log.Error(prefix+" Failed to connect GPM after retries", zap.Error(err))
		return
	}
	defer func() {
		if browser != nil {
			if err := browser.Close(); err != nil {
				log.Warn(prefix+" Failed to close browser", zap.Error(err))
			}
		}
		gpmClient.StopProfile(profileID)
	}()

	c.crawlSearchWithMonitoring(browser, context, keywords, pw, gpmClient, profileID, log)

	log.Info("========================================")
	log.Info(prefix + " Profile finished")
	log.Info("========================================")
}

// connectGPMWithRetry attempts to connect to GPM with retry logic
func (c *Crawler) connectGPMWithRetry(pw *playwright.Playwright, gpmClient *gpm.Client, profileID string, log *zap.Logger, maxRetries int) (playwright.Browser, playwright.BrowserContext, error) {
	var browser playwright.Browser
	var context playwright.BrowserContext
	var err error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		browser, context, err = c.connectGPM(pw, gpmClient, profileID, log)
		if err == nil {
			if attempt > 1 {
				log.Info("Connected to GPM successfully", zap.Int("attempt", attempt))
			}
			return browser, context, nil
		}

		log.Warn("Failed to connect to GPM",
			zap.Int("attempt", attempt),
			zap.Int("max_retries", maxRetries),
			zap.Error(err),
		)

		if attempt < maxRetries {
			// Clean up failed connection
			if browser != nil {
				browser.Close()
			}
			gpmClient.StopProfile(profileID)

			time.Sleep(5 * time.Second)
		}
	}

	return nil, nil, fmt.Errorf("failed to connect GPM after %d attempts: %w", maxRetries, err)
}

// crawlSearchWithMonitoring wraps crawlSearch with browser connection monitoring
func (c *Crawler) crawlSearchWithMonitoring(browser playwright.Browser, context playwright.BrowserContext, keywords []string, pw *playwright.Playwright, gpmClient *gpm.Client, profileID string, log *zap.Logger) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("Panic recovered in crawlSearch", zap.Any("panic", r))
		}
	}()

	for {
		// Check if browser is still connected
		if !c.isBrowserConnected(browser, log) {
			log.Warn("Browser disconnected, attempting to reconnect...")
			newBrowser, newContext, err := c.connectGPMWithRetry(pw, gpmClient, profileID, log, 3)
			if err != nil {
				log.Error("Failed to reconnect GPM", zap.Error(err))
				return
			}
			browser = newBrowser
			context = newContext
			log.Info("Reconnected to GPM successfully")
		}

		c.crawlSearch(context, keywords, log)
		return
	}
}

// isBrowserConnected checks if browser connection is still alive
func (c *Crawler) isBrowserConnected(browser playwright.Browser, log *zap.Logger) bool {
	defer func() {
		if r := recover(); r != nil {
			log.Warn("Browser connection check failed", zap.Any("panic", r))
		}
	}()

	// Try to get contexts - if this fails, browser is disconnected
	contexts := browser.Contexts()
	return len(contexts) > 0
}

func (c *Crawler) connectGPM(pw *playwright.Playwright, gpmClient *gpm.Client, profileID string, log *zap.Logger) (playwright.Browser, playwright.BrowserContext, error) {
	wsURL, err := gpmClient.StartProfile(profileID)
	if err != nil {
		return nil, nil, err
	}

	browser, err := pw.Chromium.ConnectOverCDP(wsURL)
	if err != nil {
		gpmClient.StopProfile(profileID)
		return nil, nil, fmt.Errorf("failed to connect CDP: %w", err)
	}

	contexts := browser.Contexts()
	if len(contexts) == 0 {
		browser.Close()
		gpmClient.StopProfile(profileID)
		return nil, nil, fmt.Errorf("no browser context found from GPM")
	}

	return browser, contexts[0], nil
}

func (c *Crawler) crawlSearch(context playwright.BrowserContext, keywords []string, log *zap.Logger) {
	total := len(keywords)
	i := 0

	for i < total {
		batchSize := utils.RandInt(c.cfg.BatchMin, c.cfg.BatchMax)
		batch := keywords[i:min(i+batchSize, total)]

		c.log.Info("----------------------------------------")
		c.log.Info("New session started",
			zap.Int("batch_size", len(batch)),
			zap.Strings("keywords", batch),
		)
		c.log.Info("----------------------------------------")

		// Process each keyword with its own tab
		for keywordIdx, keyword := range batch {
			c.log.Info(">>> Processing keyword",
				zap.Int("keyword_number", keywordIdx+1),
				zap.Int("total_in_batch", len(batch)),
				zap.String("keyword", keyword),
			)

			// Create new page for each keyword
			page, err := c.createPageWithRetry(context, 3)
			if err != nil {
				c.log.Error("Failed to create page after retries", zap.String("keyword", keyword), zap.Error(err))
				continue
			}

			// Crawl single keyword with dedicated page
			c.crawlKeyword(page, keyword)

			// Close page immediately after crawling
			if err := page.Close(); err != nil {
				c.log.Warn("Failed to close page", zap.String("keyword", keyword), zap.Error(err))
			}

			// Sleep between keywords (except last one in batch)
			if keywordIdx < len(batch)-1 {
				sleepSec := utils.RandInt(c.cfg.SleepMinKeyword, c.cfg.SleepMaxKeyword)
				c.log.Info("⏳ Sleeping before next keyword",
					zap.Int("seconds", sleepSec),
					zap.String("next_keyword", batch[keywordIdx+1]),
				)
				time.Sleep(time.Duration(sleepSec) * time.Second)
			}
		}

		// Rest between sessions
		i += batchSize
		if i < total {
			restSec := utils.RandInt(c.cfg.RestMinSession, c.cfg.RestMaxSession)
			c.log.Info("----------------------------------------")
			c.log.Info("Session completed, resting before next session",
				zap.Int("seconds", restSec),
				zap.Int("keywords_completed", i),
				zap.Int("keywords_remaining", total-i),
			)
			c.log.Info("----------------------------------------")
			time.Sleep(time.Duration(restSec) * time.Second)
		}
	}

	c.log.Info("✅ All keywords crawled for this profile")
}

// createPageWithRetry attempts to create a new page with retry logic
func (c *Crawler) createPageWithRetry(context playwright.BrowserContext, maxRetries int) (playwright.Page, error) {
	var page playwright.Page
	var err error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		page, err = context.NewPage()
		if err == nil {
			return page, nil
		}

		// If target closed, no point retrying - browser is gone
		if containsAny(err.Error(), []string{"target closed", "Target page", "browser has been closed"}) {
			return nil, fmt.Errorf("browser context closed, cannot create page: %w", err)
		}

		c.log.Warn("Failed to create page",
			zap.Int("attempt", attempt),
			zap.Int("max_retries", maxRetries),
			zap.Error(err),
		)

		if attempt < maxRetries {
			time.Sleep(2 * time.Second)
		}
	}

	return nil, fmt.Errorf("failed to create page after %d attempts: %w", maxRetries, err)
}

// crawlKeyword crawls a single keyword with its dedicated page
func (c *Crawler) crawlKeyword(page playwright.Page, keyword string) {
	videosByKeyword := make(map[string][]map[string]any)
	var mu sync.Mutex

	// Setup response interceptor for this page
	page.On("response", func(res playwright.Response) {
		if !containsAny(res.URL(), []string{searchAPI}) {
			return
		}

		go func(res playwright.Response, kw string) {
			var body map[string]any
			if err := res.JSON(&body); err != nil || body == nil {
				return
			}
			if statusCode, ok := body["status_code"].(float64); !ok || statusCode != 0 {
				return
			}
			items, _ := body["item_list"].([]any)

			mu.Lock()
			defer mu.Unlock()
			seen := make(map[string]bool)
			for _, existing := range videosByKeyword[kw] {
				if id, ok := existing["id"].(string); ok {
					seen[id] = true
				}
			}
			newCount := 0
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
				videosByKeyword[kw] = append(videosByKeyword[kw], item)
				newCount++
			}
			if newCount > 0 {
				c.log.Info("   📥 Videos received",
					zap.String("keyword", kw),
					zap.Int("new", newCount),
					zap.Int("total", len(videosByKeyword[kw])),
				)
			}
		}(res, keyword)
	})

	// Navigate to TikTok home first
	if _, err := page.Goto(tiktokURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	}); err != nil {
		c.log.Warn("Failed to load TikTok home", zap.String("keyword", keyword), zap.Error(err))
		return
	}
	utils.Sleep(4000, 7000)
	_ = utils.RandomMouseMove(page)
	utils.Sleep(500, 1500)

	// Navigate to search page
	encoded := url.QueryEscape(keyword)
	ts := time.Now().UnixMilli()
	searchURL := fmt.Sprintf("%s/search/video?q=%s&t=%d", tiktokURL, encoded, ts)

	if _, err := page.Goto(searchURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	}); err != nil {
		c.log.Warn("Failed to navigate to search", zap.String("keyword", keyword), zap.Error(err))
		return
	}
	utils.Sleep(6000, 9000)

	// Scroll and interact
	scrollTimes := utils.RandInt(1, 4)
	_ = utils.HumanScroll(page, scrollTimes)
	_ = utils.RandomViewVideo(page)

	utils.Sleep(1500, 2500)

	// Get collected videos
	mu.Lock()
	items := videosByKeyword[keyword]
	mu.Unlock()

	c.log.Info("   ✅ Keyword completed",
		zap.String("keyword", keyword),
		zap.Int("videos_collected", len(items)),
	)

	// Parse and save results
	results := c.parseVideos(keyword, items)
	if len(results) > 0 {
		c.saveToFile(keyword, results)
	}
}

func (c *Crawler) parseVideos(keyword string, items []map[string]any) []models.VideoItem {
	nowTs := time.Now().Unix()
	cutoff := nowTs - sevenDays
	skipped := 0
	var results []models.VideoItem

	for _, item := range items {
		pubTime := int64(toFloat(item["createTime"]))
		if pubTime < cutoff {
			skipped++
			continue
		}
		author, _ := item["author"].(map[string]any)
		stats, _ := item["stats"].(map[string]any)

		v := models.VideoItem{
			Keyword:     keyword,
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

		results = append(results, v)
	}

	if skipped > 0 {
		c.log.Info("   📊 Videos parsed",
			zap.String("keyword", keyword),
			zap.Int("valid", len(results)),
			zap.Int("skipped_old", skipped),
		)
	}

	return results
}

func (c *Crawler) saveToFile(keyword string, videos []models.VideoItem) {
	// Convert to TiktokPost format
	var posts []parser.TiktokPost
	for _, v := range videos {
		post := parser.FromVideoItem(v)
		posts = append(posts, post)
	}

	// Post to API (unclassified)
	if err := c.apiClient.PostUnclassified(posts); err != nil {
		c.log.Error("   ❌ Failed to post to API", zap.String("keyword", keyword), zap.Error(err))
		return
	}

	c.log.Info("   ✅ Posted to API",
		zap.String("keyword", keyword),
		zap.Int("count", len(posts)),
	)
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
