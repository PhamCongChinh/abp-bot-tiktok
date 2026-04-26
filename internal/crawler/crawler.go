package crawler

import (
	"abp-bot-tiktok/internal/models"
	"abp-bot-tiktok/internal/repository"
	"abp-bot-tiktok/internal/utils"
	"abp-bot-tiktok/pkg/config"
	"abp-bot-tiktok/pkg/gpm"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
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
}

func New(cfg *config.Config, log *zap.Logger, videoRepo *repository.VideoRepository) *Crawler {
	return &Crawler{
		cfg:       cfg,
		log:       log,
		videoRepo: videoRepo,
	}
}

func (c *Crawler) Run() {
	c.log.Info("Crawl cycle started")

	if !c.cfg.UseGPM {
		c.log.Error("GPM config required. Set GPM_API and PROFILE_IDS in .env")
		return
	}

	numProfiles := len(c.cfg.ProfileIDs)
	c.log.Info("Running with profiles",
		zap.Int("profiles", numProfiles),
		zap.Int("total_keywords", len(c.cfg.Keywords)),
	)

	// Split keywords evenly across profiles
	chunks := splitKeywords(c.cfg.Keywords, numProfiles)
	for i, chunk := range chunks {
		c.log.Info("Keyword chunk assigned",
			zap.Int("profile_index", i),
			zap.String("profile_id", c.cfg.ProfileIDs[i]),
			zap.Int("keywords", len(chunk)),
		)
	}

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
	c.log.Info("Crawl cycle finished")
}

func splitKeywords(keywords []string, n int) [][]string {
	chunks := make([][]string, n)
	for i, kw := range keywords {
		chunks[i%n] = append(chunks[i%n], kw)
	}
	return chunks
}

func (c *Crawler) runProfile(profileID string, keywords []string, idx int) {
	log := c.log.With(zap.Int("profile_index", idx), zap.String("profile_id", profileID))
	log.Info("Profile starting", zap.Int("keywords", len(keywords)))

	pw, err := playwright.Run()
	if err != nil {
		log.Error("Could not start playwright", zap.Error(err))
		return
	}
	defer pw.Stop()

	gpmClient := gpm.NewClient(c.cfg.GPMAPI, log)
	browser, context, err := c.connectGPM(pw, gpmClient, profileID, log)
	if err != nil {
		log.Error("Failed to connect GPM", zap.Error(err))
		return
	}
	defer func() {
		if browser != nil {
			browser.Close()
		}
		gpmClient.StopProfile(profileID)
	}()

	c.crawlSearch(context, keywords)
	log.Info("Profile finished")
}

func (c *Crawler) connectGPM(pw *playwright.Playwright, gpmClient *gpm.Client, profileID string, log *zap.Logger) (playwright.Browser, playwright.BrowserContext, error) {
	wsURL, err := gpmClient.StartProfile(profileID)
	if err != nil {
		return nil, nil, err
	}

	log.Info("Connecting to GPM via CDP", zap.String("ws_url", wsURL))
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

	log.Info("Connected to GPM profile successfully", zap.Int("contexts", len(contexts)))
	return browser, contexts[0], nil
}

func (c *Crawler) SetKeywords(keywords []string) {
	c.cfg.Keywords = keywords
}

func (c *Crawler) crawlSearch(context playwright.BrowserContext, keywords []string) {
	total := len(keywords)
	i := 0

	for i < total {
		batchSize := utils.RandInt(c.cfg.BatchMin, c.cfg.BatchMax)
		batch := keywords[i:min(i+batchSize, total)]
		c.log.Info("New session", zap.Int("keywords", len(batch)))

		page, err := context.NewPage()
		if err != nil {
			c.log.Error("Could not create page", zap.Error(err))
			i += batchSize
			continue
		}

		videosByKeyword := make(map[string][]map[string]any)
		var mu sync.Mutex
		currentKeyword := ""

		page.On("response", func(res playwright.Response) {
			mu.Lock()
			kw := currentKeyword
			mu.Unlock()

			if kw == "" {
				return
			}
			if !containsAny(res.URL(), []string{searchAPI}) {
				return
			}

			go func(res playwright.Response, kw string) {
				c.log.Debug("API response intercepted",
					zap.String("keyword", kw),
					zap.String("url", res.URL()),
				)

				var body map[string]any
				if err := res.JSON(&body); err != nil || body == nil {
					c.log.Warn("Failed to parse API response body", zap.String("keyword", kw), zap.Error(err))
					return
				}
				if statusCode, ok := body["status_code"].(float64); !ok || statusCode != 0 {
					c.log.Warn("API returned non-zero status",
						zap.String("keyword", kw),
						zap.Any("status_code", body["status_code"]),
					)
					return
				}
				items, _ := body["item_list"].([]any)
				c.log.Info("API batch received",
					zap.String("keyword", kw),
					zap.Int("items_in_batch", len(items)),
				)

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
				c.log.Info("New videos added to buffer",
					zap.String("keyword", kw),
					zap.Int("new", newCount),
					zap.Int("total_buffered", len(videosByKeyword[kw])),
				)
			}(res, kw)
		})

		if _, err := page.Goto(tiktokURL, playwright.PageGotoOptions{
			WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		}); err != nil {
			c.log.Warn("Failed to load TikTok home", zap.Error(err))
		}
		utils.Sleep(4000, 7000)
		_ = utils.RandomMouseMove(page)
		utils.Sleep(500, 1500)

		for _, keyword := range batch {
			c.log.Info("Searching keyword", zap.String("keyword", keyword))

			mu.Lock()
			currentKeyword = keyword
			videosByKeyword[keyword] = nil
			mu.Unlock()

			encoded := url.QueryEscape(keyword)
			ts := time.Now().UnixMilli()
			searchURL := fmt.Sprintf("%s/search/video?q=%s&t=%d", tiktokURL, encoded, ts)

			if _, err := page.Goto(searchURL, playwright.PageGotoOptions{
				WaitUntil: playwright.WaitUntilStateDomcontentloaded,
			}); err != nil {
				c.log.Warn("Failed to navigate to search", zap.String("keyword", keyword), zap.Error(err))
				continue
			}
			utils.Sleep(6000, 9000)

			scrollTimes := utils.RandInt(1, 4)
			_ = utils.HumanScroll(page, scrollTimes)
			_ = utils.RandomViewVideo(page)

			utils.Sleep(1500, 2500)

			mu.Lock()
			items := videosByKeyword[keyword]
			mu.Unlock()

			c.log.Info("Videos collected", zap.String("keyword", keyword), zap.Int("count", len(items)))

			results := c.parseVideos(keyword, items)
			if len(results) > 0 {
				// Save to MongoDB (commented out)
				// if c.videoRepo != nil {
				// 	if err := c.videoRepo.BulkUpsert(results); err != nil {
				// 		c.log.Error("Failed to save to MongoDB", zap.String("keyword", keyword), zap.Error(err))
				// 	} else {
				// 		c.log.Info("✅ Saved to MongoDB", zap.String("keyword", keyword), zap.Int("count", len(results)))
				// 	}
				// }
				// Save to JSON file only
				c.saveToFile(keyword, results)
			}

			mu.Lock()
			currentKeyword = ""
			mu.Unlock()

			sleepSec := utils.RandInt(c.cfg.SleepMinKeyword, c.cfg.SleepMaxKeyword)
			c.log.Info("Waiting before next keyword", zap.Int("seconds", sleepSec))
			time.Sleep(time.Duration(sleepSec) * time.Second)
		}

		_ = page.Close()

		restSec := utils.RandInt(c.cfg.RestMinSession, c.cfg.RestMaxSession)
		c.log.Info("Resting before next session", zap.Int("seconds", restSec))
		time.Sleep(time.Duration(restSec) * time.Second)

		i += batchSize
	}

	c.log.Info("Done crawling all keywords")
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

		c.log.Info("📹 Video parsed",
			zap.String("keyword", keyword),
			zap.String("video_id", v.VideoID),
			zap.String("author", v.AuthName),
			zap.String("unique_id", "@"+v.UniqueID),
			zap.String("desc", truncate(v.Description, 80)),
			zap.Int64("views", v.Views),
			zap.Int64("likes", v.Reactions),
			zap.Int64("comments", v.Comments),
			zap.Int64("shares", v.Shares),
			zap.String("pub_time", time.Unix(v.PubTime, 0).Format("2006-01-02 15:04:05")),
		)

		results = append(results, v)
	}

	c.log.Info("Parse summary",
		zap.String("keyword", keyword),
		zap.Int("parsed", len(results)),
		zap.Int("skipped_old", skipped),
	)
	return results
}

func (c *Crawler) saveToFile(keyword string, videos []models.VideoItem) {
	if err := os.MkdirAll(c.cfg.OutputDir, 0755); err != nil {
		c.log.Error("Failed to create output dir", zap.Error(err))
		return
	}
	date := time.Now().Format("2006-01-02")
	safe := url.QueryEscape(keyword)
	filename := filepath.Join(c.cfg.OutputDir, fmt.Sprintf("keyword_%s_%s.json", safe, date))

	f, err := os.Create(filename)
	if err != nil {
		c.log.Error("Failed to create file", zap.Error(err))
		return
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	_ = enc.Encode(videos)

	c.log.Info("💾 Saved to file", zap.String("file", filename), zap.Int("count", len(videos)))
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
