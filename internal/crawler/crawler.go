package crawler

import (
	"abp-bot-tiktok/internal/models"
	"abp-bot-tiktok/internal/utils"
	"abp-bot-tiktok/pkg/config"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/playwright-community/playwright-go"
	"go.uber.org/zap"
)

//nolint:unused
const (
	tiktokURL = "https://www.tiktok.com"
	searchAPI = "/api/search/item/full/"
	oneMonth  = 15 * 24 * 60 * 60
)

type Crawler struct {
	cfg *config.Config
	log *zap.Logger
}

func New(cfg *config.Config, log *zap.Logger) *Crawler {
	return &Crawler{cfg: cfg, log: log}
}

// crawlKeyword scrapes TikTok search results for the given keyword via an
// already-connected Playwright page. Retained as dormant dead code — full
// wiring is done in T11.
//
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

	results := parseVideos(keyword, items)

	duration := time.Since(startTime)
	log.Sugar().Infof("%s   %q -> %d videos | ⏱️ %s", tag, keyword, len(results), duration.Round(time.Second))
}

//nolint:unused
func parseVideos(keyword string, items []map[string]any) []models.VideoItem {
	nowTs := time.Now().Unix()
	cutoff := nowTs - oneMonth
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

//nolint:unused
func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

//nolint:unused
func toFloat(v any) float64 {
	if v == nil {
		return 0
	}
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

//nolint:unused
func mapGet(m map[string]any, key string) any {
	if m == nil {
		return nil
	}
	return m[key]
}
