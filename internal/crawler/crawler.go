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
	"math/rand"
	"net/url"
	"sync"
	"time"

	"github.com/playwright-community/playwright-go"
	"go.uber.org/zap"
)

const (
	tiktokURL  = "https://www.tiktok.com"
	searchAPI  = "/api/search/item/full/"
	cutoffSpan = 7 * 24 * 60 * 60
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
	}
	return &Crawler{cfg: cfg, log: log, videoRepo: videoRepo, apiClient: apiClient}
}

func (c *Crawler) Run() {
	if !c.cfg.UseGPM {
		c.log.Error("GPM config required. Set GPM_API and PROFILE_IDS in .env")
		return
	}

	// Shuffle keywords each cycle for randomized crawl order
	keywords := make([]string, len(c.cfg.Keywords))
	copy(keywords, c.cfg.Keywords)
	rand.Shuffle(len(keywords), func(i, j int) {
		keywords[i], keywords[j] = keywords[j], keywords[i]
	})

	numProfiles := len(c.cfg.ProfileIDs)
	chunks := splitKeywords(keywords, numProfiles)

	var wg sync.WaitGroup
	for i, profileID := range c.cfg.ProfileIDs {
		wg.Add(1)
		go func(profileID string, keywords []string, idx int) {
			defer wg.Done()
			c.runProfile(profileID, keywords, idx)
		}(profileID, chunks[i], i)
	}
	wg.Wait()
}

func splitKeywords(keywords []string, n int) [][]string {
	chunks := make([][]string, n)
	for i, kw := range keywords {
		chunks[i%n] = append(chunks[i%n], kw)
	}
	return chunks
}

func (c *Crawler) runProfile(profileID string, keywords []string, idx int) {
	tag := fmt.Sprintf("[P%d|%s...]", idx+1, profileID[:8])
	log := c.log

	pw, err := playwright.Run()
	if err != nil {
		log.Sugar().Errorf("%s playwright error: %v", tag, err)
		return
	}
	defer pw.Stop()

	gpmClient := gpm.NewClient(c.cfg.GPMAPI, log)
	c.crawlSearch(pw, gpmClient, profileID, keywords, log, tag)
}

func (c *Crawler) connectGPMWithRetry(pw *playwright.Playwright, gpmClient *gpm.Client, profileID string, log *zap.Logger, maxRetries int) (playwright.Browser, playwright.BrowserContext, error) {
	var browser playwright.Browser
	var context playwright.BrowserContext
	var err error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		browser, context, err = c.connectGPM(pw, gpmClient, profileID, log)
		if err == nil {
			return browser, context, nil
		}
		if browser != nil {
			browser.Close()
		}
		gpmClient.StopProfile(profileID)
		if attempt < maxRetries {
			time.Sleep(5 * time.Second)
		}
	}
	return nil, nil, fmt.Errorf("failed after %d attempts: %w", maxRetries, err)
}

func (c *Crawler) connectGPM(pw *playwright.Playwright, gpmClient *gpm.Client, profileID string, log *zap.Logger) (playwright.Browser, playwright.BrowserContext, error) {
	wsURL, err := gpmClient.StartProfile(profileID)
	if err != nil {
		return nil, nil, err
	}
	browser, err := pw.Chromium.ConnectOverCDP(wsURL)
	if err != nil {
		gpmClient.StopProfile(profileID)
		return nil, nil, fmt.Errorf("CDP connect failed: %w", err)
	}
	contexts := browser.Contexts()
	if len(contexts) == 0 {
		browser.Close()
		gpmClient.StopProfile(profileID)
		return nil, nil, fmt.Errorf("no browser context from GPM")
	}
	return browser, contexts[0], nil
}

func (c *Crawler) crawlSearch(pw *playwright.Playwright, gpmClient *gpm.Client, profileID string, keywords []string, log *zap.Logger, tag string) {
	total := len(keywords)
	i := 0

	for i < total {
		batchSize := utils.RandInt(c.cfg.BatchMin, c.cfg.BatchMax)
		batch := keywords[i:min(i+batchSize, total)]

		for keywordIdx, keyword := range batch {
			browser, context, err := c.connectGPMWithRetry(pw, gpmClient, profileID, log, 3)
			if err != nil {
				log.Sugar().Infof("%s %q -> 0 videos pushed to API", tag, keyword)
				continue
			}

			page, err := c.createPageWithRetry(context, 3, log)
			if err != nil {
				log.Sugar().Infof("%s %q -> 0 videos pushed to API", tag, keyword)
			} else {
				c.crawlKeyword(page, keyword, log, tag)
				page.Close()
			}

			browser.Close()
			gpmClient.StopProfile(profileID)

			if keywordIdx < len(batch)-1 {
				sleepSec := utils.RandInt(c.cfg.SleepMinKeyword, c.cfg.SleepMaxKeyword)
				time.Sleep(time.Duration(sleepSec) * time.Second)
			}
		}

		i += batchSize
		if i < total {
			restSec := utils.RandInt(c.cfg.RestMinSession, c.cfg.RestMaxSession)
			time.Sleep(time.Duration(restSec) * time.Second)
		}
	}
}

func (c *Crawler) createPageWithRetry(context playwright.BrowserContext, maxRetries int, log *zap.Logger) (playwright.Page, error) {
	var page playwright.Page
	var err error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		page, err = context.NewPage()
		if err == nil {
			return page, nil
		}
		if containsAny(err.Error(), []string{"target closed", "Target page", "browser has been closed"}) {
			return nil, fmt.Errorf("browser closed: %w", err)
		}
		if attempt < maxRetries {
			time.Sleep(2 * time.Second)
		}
	}
	return nil, fmt.Errorf("failed after %d attempts: %w", maxRetries, err)
}

func (c *Crawler) crawlKeyword(page playwright.Page, keyword string, log *zap.Logger, tag string) {
	page.Route("**/*", func(route playwright.Route) {
		switch route.Request().ResourceType() {
		case "stylesheet", "font", "image", "media", "other":
			route.Abort()
		default:
			route.Continue()
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
				return
			}
			statusCode, _ := body["status_code"].(float64)
			items, _ := body["item_list"].([]any)

			// Detect rate limit / captcha
			if statusCode == 2061 || statusCode == 10000 || statusCode == -1 {
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
		log.Sugar().Infof("%s %q -> 0 videos pushed to API", tag, keyword)
		return
	}
	utils.Sleep(4000, 7000)
	_ = utils.RandomMouseMove(page)
	utils.Sleep(500, 1500)

	encoded := url.QueryEscape(keyword)
	ts := time.Now().UnixMilli()

	// Navigate to Top tab first
	topURL := fmt.Sprintf("%s/search?q=%s&t=%d", tiktokURL, encoded, ts)
	if _, err := page.Goto(topURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	}); err != nil {
		log.Sugar().Infof("%s %q -> 0 videos pushed to API", tag, keyword)
		return
	}
	utils.Sleep(3000, 5000)

	// Check if Top tab has videos, if not switch to Video tab
	hasVideos, _ := page.Evaluate(`() => {
		const items = document.querySelectorAll('[data-e2e="search-common-video"], [class*="DivItemContainer"]');
		return items.length > 0;
	}`)
	if hasVideos == nil || hasVideos == false {
		videoURL := fmt.Sprintf("%s/search/video?q=%s&t=%d", tiktokURL, encoded, ts)
		if _, err := page.Goto(videoURL, playwright.PageGotoOptions{
			WaitUntil: playwright.WaitUntilStateDomcontentloaded,
			Timeout:   playwright.Float(30000),
		}); err != nil {
			log.Sugar().Infof("%s %q -> 0 videos pushed to API", tag, keyword)
			return
		}
		utils.Sleep(4000, 6000)
	}

	// Scroll to load more videos
	scrollTimes := utils.RandInt(10, 15)
	_ = utils.HumanScroll(page, scrollTimes)

	// Random view video
	_ = utils.RandomViewVideo(page)
	utils.Sleep(1500, 2500)

	mu.Lock()
	items := collectedItems
	mu.Unlock()

	orgID := c.cfg.KeywordOrgMap[keyword]
	results := c.parseVideos(keyword, orgID, items)

	if len(results) > 0 {
		if err := c.pushToAPI(results); err != nil {
			log.Sugar().Infof("%s %q -> push to API failed: %v", tag, keyword, err)
			return
		}
	}
	log.Sugar().Infof("%s %q -> %d videos pushed to API", tag, keyword, len(results))
}

func (c *Crawler) parseVideos(keyword string, orgID int, items []map[string]any) []models.VideoItem {
	nowTs := time.Now().Unix()
	cutoff := nowTs - cutoffSpan
	var results []models.VideoItem

	for _, item := range items {
		pubTime := int64(toFloat(item["createTime"]))
		if pubTime < cutoff {
			continue
		}
		author, _ := item["author"].(map[string]any)
		stats, _ := item["stats"].(map[string]any)

		results = append(results, models.VideoItem{
			Keyword:     keyword,
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
		})
	}
	return results
}

func (c *Crawler) pushToAPI(videos []models.VideoItem) error {
	var posts []parser.TiktokPost
	for _, v := range videos {
		posts = append(posts, parser.FromVideoItem(v))
	}
	return c.apiClient.PostUnclassified(posts)
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
