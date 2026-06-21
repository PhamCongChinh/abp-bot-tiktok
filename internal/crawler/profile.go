package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/playwright-community/playwright-go"
	"go.uber.org/zap"
)

const userDetailAPI = "/api/user/detail/"

// ProfileItem holds TikTok profile data extracted from /api/user/detail/.
type ProfileItem struct {
	SourceRecordID string    // userInfo.user.id
	UniqueID       string    // userInfo.user.uniqueId
	Nickname       string    // userInfo.user.nickname
	Signature      string    // userInfo.user.signature (bio)
	Verified       bool      // userInfo.user.verified
	SecUID         string    // userInfo.user.secUid
	FollowerCount  int64     // userInfo.stats.followerCount
	FollowingCount int64     // userInfo.stats.followingCount
	HeartCount     int64     // userInfo.stats.heartCount
	VideoCount     int64     // userInfo.stats.videoCount
	RawBytes       []byte    // full response body
	FetchedAt      time.Time // UTC scrape time
}

// ProfileCrawlerIface abstracts the TikTok profile crawler for testing.
type ProfileCrawlerIface interface {
	Crawl(ctx context.Context, page playwright.Page, handle string) (*ProfileItem, error)
}

// TikTokProfileCrawler navigates to tiktok.com/@{handle} and intercepts
// /api/user/detail/ which fires on page load, then extracts profile fields.
type TikTokProfileCrawler struct {
	log *zap.Logger
}

// NewTikTokProfileCrawler creates a new profile crawler.
func NewTikTokProfileCrawler(log *zap.Logger) *TikTokProfileCrawler {
	return &TikTokProfileCrawler{log: log}
}

// Crawl navigates to tiktok.com/@{handle}, waits for /api/user/detail/ to fire,
// and returns the extracted profile. Returns an error if the endpoint doesn't
// fire within the timeout or the response cannot be parsed.
func (c *TikTokProfileCrawler) Crawl(ctx context.Context, page playwright.Page, handle string) (*ProfileItem, error) {
	result := make(chan *ProfileItem, 1)
	errCh := make(chan error, 1)

	page.On("response", func(res playwright.Response) {
		if !strings.Contains(res.URL(), userDetailAPI) {
			return
		}
		go func(res playwright.Response) {
			rawBytes, err := res.Body()
			if err != nil {
				errCh <- fmt.Errorf("response body: %w", err)
				return
			}
			item, err := extractProfileItem(rawBytes)
			if err != nil {
				errCh <- err
				return
			}
			result <- item
		}(res)
	})

	profileURL := fmt.Sprintf("https://www.tiktok.com/@%s", handle)
	if _, err := page.Goto(profileURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	}); err != nil {
		return nil, fmt.Errorf("navigate to @%s: %w", handle, err)
	}

	select {
	case item := <-result:
		return item, nil
	case err := <-errCh:
		return nil, err
	case <-time.After(15 * time.Second):
		return nil, fmt.Errorf("timeout waiting for %s response", userDetailAPI)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// extractProfileItem parses the /api/user/detail/ response body into a ProfileItem.
func extractProfileItem(rawBytes []byte) (*ProfileItem, error) {
	var body map[string]any
	if err := json.Unmarshal(rawBytes, &body); err != nil {
		return nil, fmt.Errorf("parse user detail body: %w", err)
	}

	userInfo, _ := body["userInfo"].(map[string]any)
	if userInfo == nil {
		return nil, fmt.Errorf("userInfo missing in /api/user/detail/ response")
	}

	user, _ := userInfo["user"].(map[string]any)
	stats, _ := userInfo["stats"].(map[string]any)

	if user == nil {
		return nil, fmt.Errorf("userInfo.user missing in /api/user/detail/ response")
	}

	sourceRecordID := toString(user["id"])
	if sourceRecordID == "" {
		return nil, fmt.Errorf("userInfo.user.id is empty")
	}

	verified, _ := user["verified"].(bool)

	return &ProfileItem{
		SourceRecordID: sourceRecordID,
		UniqueID:       toString(user["uniqueId"]),
		Nickname:       toString(user["nickname"]),
		Signature:      toString(user["signature"]),
		Verified:       verified,
		SecUID:         toString(user["secUid"]),
		FollowerCount:  int64(toFloat(mapGet(stats, "followerCount"))),
		FollowingCount: int64(toFloat(mapGet(stats, "followingCount"))),
		HeartCount:     int64(toFloat(mapGet(stats, "heartCount"))),
		VideoCount:     int64(toFloat(mapGet(stats, "videoCount"))),
		RawBytes:       rawBytes,
		FetchedAt:      time.Now().UTC(),
	}, nil
}
