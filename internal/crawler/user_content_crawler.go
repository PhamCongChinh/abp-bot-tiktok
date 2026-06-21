package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"abp-bot-tiktok/internal/utils"
	"github.com/playwright-community/playwright-go"
	"go.uber.org/zap"
)

const contentItemListAPI = "/api/post/item_list/"

// TikTokUserContentCrawler navigates to tiktok.com/@{handle} and intercepts
// /api/post/item_list/ with scroll-based pagination up to pageCap pages.
type TikTokUserContentCrawler struct {
	pageCap int
	log     *zap.Logger
}

// NewTikTokUserContentCrawler creates a new content crawler.
func NewTikTokUserContentCrawler(pageCap int, log *zap.Logger) *TikTokUserContentCrawler {
	return &TikTokUserContentCrawler{pageCap: pageCap, log: log}
}

// contentBatch holds one intercepted /api/post/item_list/ response.
type contentBatch struct {
	items   []ContentItem
	hasMore bool
	err     error
}

// Crawl navigates to tiktok.com/@{handle}, intercepts /api/post/item_list/
// responses, and scrolls to paginate. Returns all scraped items and the
// number of pages successfully consumed. err is non-nil on pagination failure.
func (c *TikTokUserContentCrawler) Crawl(ctx context.Context, page playwright.Page, handle string) ([]ContentItem, int, error) {
	batches := make(chan contentBatch, 20)

	page.On("response", func(res playwright.Response) {
		if !strings.Contains(res.URL(), contentItemListAPI) {
			return
		}
		go func(res playwright.Response) {
			rawBytes, err := res.Body()
			if err != nil {
				batches <- contentBatch{err: fmt.Errorf("response body: %w", err)}
				return
			}
			var body map[string]any
			if err := json.Unmarshal(rawBytes, &body); err != nil {
				c.log.Warn("parse body failed",
					zap.String("url", res.URL()),
					zap.Int("body_len", len(rawBytes)),
					zap.String("body_prefix", string(rawBytes[:min(200, len(rawBytes))])),
					zap.Error(err),
				)
				batches <- contentBatch{err: fmt.Errorf("parse body: %w", err)}
				return
			}
			items := extractContentItems(rawBytes, body)
			hasMore := toFloat(body["hasMore"]) == 1
			batches <- contentBatch{items: items, hasMore: hasMore}
		}(res)
	})

	profileURL := fmt.Sprintf("https://www.tiktok.com/@%s", handle)
	if _, err := page.Goto(profileURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	}); err != nil {
		return nil, 0, fmt.Errorf("navigate to @%s: %w", handle, err)
	}

	utils.Sleep(3000, 5000)

	var allItems []ContentItem
	pagesCompleted := 0

	for pagesCompleted < c.pageCap {
		select {
		case batch := <-batches:
			if batch.err != nil {
				if pagesCompleted > 0 {
					return allItems, pagesCompleted, batch.err
				}
				return nil, 0, batch.err
			}
			allItems = append(allItems, batch.items...)
			pagesCompleted++

			if !batch.hasMore || pagesCompleted >= c.pageCap {
				return allItems, pagesCompleted, nil
			}
			// Scroll to trigger the next /api/post/item_list/ request natively.
			if err := utils.HumanScroll(page, 3); err != nil {
				c.log.Warn("scroll failed", zap.Error(err))
			}
			utils.Sleep(2000, 4000)

		case <-time.After(15 * time.Second):
			if pagesCompleted > 0 {
				// No more batches arrived — treat as natural end of feed.
				return allItems, pagesCompleted, nil
			}
			return nil, 0, fmt.Errorf("timeout waiting for %s response", contentItemListAPI)

		case <-ctx.Done():
			return allItems, pagesCompleted, ctx.Err()
		}
	}

	return allItems, pagesCompleted, nil
}

// extractContentItems parses items from a /api/post/item_list/ response body.
// rawBytes is stored as-is per item for raw landing.
func extractContentItems(rawBytes []byte, body map[string]any) []ContentItem {
	itemList, _ := body["item_list"].([]any)
	fetchedAt := time.Now().UTC()
	var items []ContentItem
	for _, raw := range itemList {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := toString(item["id"])
		if id == "" {
			continue
		}
		stats, _ := item["stats"].(map[string]any)
		video, _ := item["video"].(map[string]any)
		author, _ := item["author"].(map[string]any)

		items = append(items, ContentItem{
			SourceRecordID: id,
			Desc:           toString(item["desc"]),
			CreateTime:     int64(toFloat(item["createTime"])),
			PlayCount:      int64(toFloat(mapGet(stats, "playCount"))),
			DiggCount:      int64(toFloat(mapGet(stats, "diggCount"))),
			CommentCount:   int64(toFloat(mapGet(stats, "commentCount"))),
			ShareCount:     int64(toFloat(mapGet(stats, "shareCount"))),
			Duration:       toFloat(mapGet(video, "duration")),
			UniqueID:       toString(mapGet(author, "uniqueId")),
			RawBytes:       rawBytes,
			FetchedAt:      fetchedAt,
		})
	}
	return items
}
