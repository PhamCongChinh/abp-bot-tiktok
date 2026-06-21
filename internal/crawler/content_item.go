package crawler

import (
	"context"
	"time"

	"abp-bot-tiktok/internal/fetcher"
	"abp-bot-tiktok/internal/landing"
	"github.com/google/uuid"
	"github.com/playwright-community/playwright-go"
)

// ContentItem holds one TikTok video extracted from /api/post/item_list/.
type ContentItem struct {
	SourceRecordID string    // item.id
	Desc           string    // item.desc
	CreateTime     int64     // item.createTime
	PlayCount      int64     // item.stats.playCount
	DiggCount      int64     // item.stats.diggCount
	CommentCount   int64     // item.stats.commentCount
	ShareCount     int64     // item.stats.shareCount
	Duration       float64   // item.video.duration
	UniqueID       string    // item.author.uniqueId (handle)
	RawBytes       []byte    // full response body from which this item was extracted
	FetchedAt      time.Time // UTC scrape time
}

// ClaimLoopIface abstracts the claim loop for testing.
type ClaimLoopIface interface {
	Poll() ([]fetcher.FetchRequest, error)
	CurrentWsURL() string
}

// WriterIface abstracts the landing writer for testing.
type WriterIface interface {
	Land(ctx context.Context, item landing.LandingItem) error
	Finalize(ctx context.Context, id uuid.UUID, status string, lastError *string, attemptsInc int) error
}

// ContentCrawlerIface abstracts the TikTok content crawler.
// The playwright.Page argument is used by the real implementation; mock
// implementations may ignore it (pass nil in tests).
type ContentCrawlerIface interface {
	Crawl(ctx context.Context, page playwright.Page, handle string) ([]ContentItem, int, error)
}
