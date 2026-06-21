package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/playwright-community/playwright-go"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	"abp-bot-tiktok/internal/fetcher"
	"abp-bot-tiktok/internal/landing"
	"abp-bot-tiktok/pkg/config"
)

// ---- test doubles ----

// stubClaimLoop satisfies ClaimLoopIface with fixed data.
type stubClaimLoop struct {
	rows  []fetcher.FetchRequest
	wsURL string
}

func (s *stubClaimLoop) Poll() ([]fetcher.FetchRequest, error) {
	return s.rows, nil
}

func (s *stubClaimLoop) CurrentWsURL() string { return s.wsURL }

// stubContentCrawler satisfies ContentCrawlerIface with canned responses.
type stubContentCrawler struct {
	items         []ContentItem
	pagesComplete int
	err           error
}

func (s *stubContentCrawler) Crawl(_ context.Context, _ playwright.Page, _ string) ([]ContentItem, int, error) {
	return s.items, s.pagesComplete, s.err
}

// stubProfileCrawler satisfies ProfileCrawlerIface with canned responses.
type stubProfileCrawler struct {
	item *ProfileItem
	err  error
}

func (s *stubProfileCrawler) Crawl(_ context.Context, _ playwright.Page, _ string) (*ProfileItem, error) {
	return s.item, s.err
}

// ---- helpers ----

func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — skipping DB-dependent test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("postgres ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func openTestWriter(t *testing.T, pool *pgxpool.Pool) *landing.Writer {
	t.Helper()
	endpoint := os.Getenv("MINIO_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("MINIO_TEST_ENDPOINT not set — skipping integration test")
	}
	access := os.Getenv("MINIO_TEST_ACCESS")
	if access == "" {
		access = "minioadmin"
	}
	secret := os.Getenv("MINIO_TEST_SECRET")
	if secret == "" {
		secret = "minioadmin"
	}
	bucket := os.Getenv("MINIO_TEST_BUCKET")
	if bucket == "" {
		bucket = "test-raw"
	}
	w, err := landing.New(pool, endpoint, access, secret, bucket, false)
	if err != nil {
		t.Fatalf("landing.New: %v", err)
	}
	return w
}

func seedSocialAccount(t *testing.T, pool *pgxpool.Pool, platformUserID, handle string) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		INSERT INTO canonical.social_account (platform, platform_user_id, handle)
		VALUES ('tiktok', $1, $2)
		ON CONFLICT (platform, platform_user_id) DO UPDATE SET handle = $2
	`, platformUserID, handle)
	if err != nil {
		t.Fatalf("seed social_account: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx,
			`DELETE FROM canonical.social_account WHERE platform='tiktok' AND platform_user_id=$1`, platformUserID)
	})
}

func seedFetchRequest(t *testing.T, pool *pgxpool.Pool, sourceID, target, scope string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO raw.fetch_request (source_id, platform, target, scope, status)
		VALUES ($1, 'tiktok', $2, $3, 'claimed')
		RETURNING id::text
	`, sourceID, target, scope).Scan(&id)
	if err != nil {
		t.Fatalf("seed fetch_request: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM raw.fetch_request WHERE id::text=$1`, id)
	})
	return id
}

func cleanupRecords(t *testing.T, pool *pgxpool.Pool, sourceID string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM raw.record WHERE source_id=$1`, sourceID)
	})
}

func testCrawlerConfig(sourceID string) *config.Config {
	return &config.Config{
		SourceID:             sourceID,
		TikTokContentPageCap: 10,
		TikTokProfileIDs:     []string{"profile-1"},
	}
}

func makeContentItems(rawBytes []byte, ids ...string) []ContentItem {
	items := make([]ContentItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, ContentItem{
			SourceRecordID: id,
			Desc:           fmt.Sprintf("video %s", id),
			CreateTime:     time.Now().Unix(),
			RawBytes:       rawBytes,
			FetchedAt:      time.Now().UTC(),
		})
	}
	return items
}

// ---- integration tests ----

// TestCrawler_HappyPath_ContentScope seeds a fetch_request with scope='content',
// runs Crawler with a stub returning 2 items, and asserts raw.record rows + status='landed'.
func TestCrawler_HappyPath_ContentScope(t *testing.T) {
	pool := openTestPool(t)
	writer := openTestWriter(t, pool)
	ctx := context.Background()

	sourceID := "scraper_tiktok_T11_happy"
	target := "tt_uid_happy_001"
	handle := "happy_user_001"

	seedSocialAccount(t, pool, target, handle)
	rowID := seedFetchRequest(t, pool, sourceID, target, "content")
	cleanupRecords(t, pool, sourceID)

	rawBytes := []byte(`{"item_list":[{"id":"v1","desc":"test video 1"},{"id":"v2","desc":"test video 2"}],"hasMore":0}`)
	items := makeContentItems(rawBytes, "v1", "v2")

	stub := &stubClaimLoop{
		rows:  []fetcher.FetchRequest{{ID: rowID, Target: target, Scope: "content", SourceID: sourceID}},
		wsURL: "ws://stub:9999",
	}
	contentStub := &stubContentCrawler{items: items, pagesComplete: 1}
	c := newWithDeps(stub, nil, writer, contentStub, nil, pool, testCrawlerConfig(sourceID), zaptest.NewLogger(t))
	c.Handle(stub.rows)

	// Assert two raw.record rows with process_status='landed'.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM raw.record WHERE source_id=$1 AND process_status='landed'`, sourceID,
	).Scan(&count); err != nil {
		t.Fatalf("query raw.record: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 raw.record rows, got %d", count)
	}

	// Assert fetch_request.status='landed' with NULL last_error.
	var status string
	var lastError *string
	if err := pool.QueryRow(ctx,
		`SELECT status, last_error FROM raw.fetch_request WHERE id::text=$1`, rowID,
	).Scan(&status, &lastError); err != nil {
		t.Fatalf("query fetch_request: %v", err)
	}
	if status != "landed" {
		t.Errorf("status = %q, want %q", status, "landed")
	}
	if lastError != nil {
		t.Errorf("last_error = %q, want NULL", *lastError)
	}
}

// TestCrawler_NullHandle verifies null handle → status='failed', last_error='handle_unknown'.
func TestCrawler_NullHandle(t *testing.T) {
	pool := openTestPool(t)
	writer := openTestWriter(t, pool)
	ctx := context.Background()

	sourceID := "scraper_tiktok_T11_nohandle"
	target := "tt_uid_no_account"

	// No canonical.social_account row → handle resolution fails.
	// Delete any stale row first.
	_, _ = pool.Exec(ctx, `DELETE FROM canonical.social_account WHERE platform='tiktok' AND platform_user_id=$1`, target)
	rowID := seedFetchRequest(t, pool, sourceID, target, "content")

	stub := &stubClaimLoop{
		rows:  []fetcher.FetchRequest{{ID: rowID, Target: target, Scope: "content", SourceID: sourceID}},
		wsURL: "ws://stub:9999",
	}
	contentStub := &stubContentCrawler{}
	c := newWithDeps(stub, nil, writer, contentStub, nil, pool, testCrawlerConfig(sourceID), zap.NewNop())
	c.Handle(stub.rows)

	var status string
	var lastError *string
	if err := pool.QueryRow(ctx,
		`SELECT status, last_error FROM raw.fetch_request WHERE id::text=$1`, rowID,
	).Scan(&status, &lastError); err != nil {
		t.Fatalf("query fetch_request: %v", err)
	}
	if status != "failed" {
		t.Errorf("status = %q, want %q", status, "failed")
	}
	if lastError == nil || *lastError != "handle_unknown" {
		t.Errorf("last_error = %v, want %q", lastError, "handle_unknown")
	}
}

// TestCrawler_PartialPaginationFailure verifies edge case (b): first batch lands,
// second batch fails → status='landed' with non-null last_error starting with "partial:".
func TestCrawler_PartialPaginationFailure(t *testing.T) {
	pool := openTestPool(t)
	writer := openTestWriter(t, pool)
	ctx := context.Background()

	sourceID := "scraper_tiktok_T11_partial"
	target := "tt_uid_partial_001"
	handle := "partial_user_001"

	seedSocialAccount(t, pool, target, handle)
	rowID := seedFetchRequest(t, pool, sourceID, target, "content")
	cleanupRecords(t, pool, sourceID)

	rawBytes := []byte(`{"item_list":[{"id":"vp1","desc":"partial video"}],"hasMore":1}`)
	items := makeContentItems(rawBytes, "vp1")

	stub := &stubClaimLoop{
		rows:  []fetcher.FetchRequest{{ID: rowID, Target: target, Scope: "content", SourceID: sourceID}},
		wsURL: "ws://stub:9999",
	}
	contentStub := &stubContentCrawler{
		items:         items,
		pagesComplete: 1,
		err:           fmt.Errorf("scroll timeout on page 2"),
	}
	c := newWithDeps(stub, nil, writer, contentStub, nil, pool, testCrawlerConfig(sourceID), zap.NewNop())
	c.Handle(stub.rows)

	// First batch's raw.record row should be present.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM raw.record WHERE source_id=$1`, sourceID,
	).Scan(&count); err != nil {
		t.Fatalf("query raw.record: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 raw.record row for partial success, got %d", count)
	}

	// fetch_request.status='landed' with non-null last_error starting with "partial:".
	var status string
	var lastError *string
	if err := pool.QueryRow(ctx,
		`SELECT status, last_error FROM raw.fetch_request WHERE id::text=$1`, rowID,
	).Scan(&status, &lastError); err != nil {
		t.Fatalf("query fetch_request: %v", err)
	}
	if status != "landed" {
		t.Errorf("status = %q, want %q", status, "landed")
	}
	if lastError == nil {
		t.Fatal("last_error should be non-null on partial failure")
	}
	if len(*lastError) < 8 || (*lastError)[:8] != "partial:" {
		t.Errorf("last_error = %q, want prefix %q", *lastError, "partial:")
	}
}

// TestCrawler_FullCrawlFailure verifies zero items scraped → status='failed'.
func TestCrawler_FullCrawlFailure(t *testing.T) {
	pool := openTestPool(t)
	writer := openTestWriter(t, pool)
	ctx := context.Background()

	sourceID := "scraper_tiktok_T11_fullfail"
	target := "tt_uid_fullfail"
	handle := "fullfail_user"

	seedSocialAccount(t, pool, target, handle)
	rowID := seedFetchRequest(t, pool, sourceID, target, "content")

	stub := &stubClaimLoop{
		rows:  []fetcher.FetchRequest{{ID: rowID, Target: target, Scope: "content", SourceID: sourceID}},
		wsURL: "ws://stub:9999",
	}
	contentStub := &stubContentCrawler{err: fmt.Errorf("navigate failed: timeout")}
	c := newWithDeps(stub, nil, writer, contentStub, nil, pool, testCrawlerConfig(sourceID), zap.NewNop())
	c.Handle(stub.rows)

	var status string
	var lastError *string
	if err := pool.QueryRow(ctx,
		`SELECT status, last_error FROM raw.fetch_request WHERE id::text=$1`, rowID,
	).Scan(&status, &lastError); err != nil {
		t.Fatalf("query fetch_request: %v", err)
	}
	if status != "failed" {
		t.Errorf("status = %q, want %q", status, "failed")
	}
	if lastError == nil {
		t.Error("last_error should be non-null on full failure")
	}
}

// ---- unit tests for extractContentItems ----

// TestExtractContentItems_HappyPath verifies parsing of a /api/post/item_list/ response.
func TestExtractContentItems_HappyPath(t *testing.T) {
	raw := []byte(`{
		"item_list": [
			{
				"id": "123456",
				"desc": "my tiktok video",
				"createTime": 1700000000,
				"stats": {"playCount": 1000, "diggCount": 50, "commentCount": 10, "shareCount": 5},
				"video": {"duration": 15.5},
				"author": {"uniqueId": "myhandle"}
			}
		],
		"hasMore": 0
	}`)

	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	items := extractContentItems(raw, body)

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	item := items[0]
	if item.SourceRecordID != "123456" {
		t.Errorf("SourceRecordID = %q, want %q", item.SourceRecordID, "123456")
	}
	if item.Desc != "my tiktok video" {
		t.Errorf("Desc = %q, want %q", item.Desc, "my tiktok video")
	}
	if item.CreateTime != 1700000000 {
		t.Errorf("CreateTime = %d, want 1700000000", item.CreateTime)
	}
	if item.PlayCount != 1000 {
		t.Errorf("PlayCount = %d, want 1000", item.PlayCount)
	}
	if item.DiggCount != 50 {
		t.Errorf("DiggCount = %d, want 50", item.DiggCount)
	}
	if item.CommentCount != 10 {
		t.Errorf("CommentCount = %d, want 10", item.CommentCount)
	}
	if item.ShareCount != 5 {
		t.Errorf("ShareCount = %d, want 5", item.ShareCount)
	}
	if item.Duration != 15.5 {
		t.Errorf("Duration = %f, want 15.5", item.Duration)
	}
	if item.UniqueID != "myhandle" {
		t.Errorf("UniqueID = %q, want %q", item.UniqueID, "myhandle")
	}
	if string(item.RawBytes) != string(raw) {
		t.Error("RawBytes should equal the full response body")
	}
}

// TestExtractContentItems_EmptyList verifies empty item_list returns empty slice.
func TestExtractContentItems_EmptyList(t *testing.T) {
	raw := []byte(`{"item_list":[],"hasMore":0}`)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	items := extractContentItems(raw, body)
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

// TestExtractContentItems_SkipsNoID verifies items with empty or missing id are skipped.
func TestExtractContentItems_SkipsNoID(t *testing.T) {
	raw := []byte(`{"item_list":[{"desc":"no id"},{"id":"","desc":"empty id"},{"id":"ok1"}],"hasMore":0}`)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	items := extractContentItems(raw, body)
	if len(items) != 1 {
		t.Errorf("expected 1 item (with id='ok1'), got %d", len(items))
	}
	if items[0].SourceRecordID != "ok1" {
		t.Errorf("SourceRecordID = %q, want %q", items[0].SourceRecordID, "ok1")
	}
}

// ---- unit tests for resolveHandle ----

// TestResolveHandle_HappyPath verifies resolveHandle returns the handle when present.
func TestResolveHandle_HappyPath(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()
	target := "tt_uid_resolve_ok_T11"

	_, _ = pool.Exec(ctx,
		`DELETE FROM canonical.social_account WHERE platform='tiktok' AND platform_user_id=$1`, target)
	_, err := pool.Exec(ctx, `
		INSERT INTO canonical.social_account (platform, platform_user_id, handle)
		VALUES ('tiktok', $1, 'mygoodhandle')
	`, target)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx,
			`DELETE FROM canonical.social_account WHERE platform='tiktok' AND platform_user_id=$1`, target)
	})

	c := &Crawler{pool: pool, cfg: testCrawlerConfig("test"), log: zap.NewNop()}
	handle, err := c.resolveHandle(ctx, target)
	if err != nil {
		t.Fatalf("resolveHandle: %v", err)
	}
	if handle != "mygoodhandle" {
		t.Errorf("handle = %q, want %q", handle, "mygoodhandle")
	}
}

// TestResolveHandle_NullHandle verifies resolveHandle errors when handle is NULL.
func TestResolveHandle_NullHandle(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()
	target := "tt_uid_null_handle_T11"

	_, _ = pool.Exec(ctx,
		`DELETE FROM canonical.social_account WHERE platform='tiktok' AND platform_user_id=$1`, target)
	_, err := pool.Exec(ctx, `
		INSERT INTO canonical.social_account (platform, platform_user_id, handle)
		VALUES ('tiktok', $1, NULL)
	`, target)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx,
			`DELETE FROM canonical.social_account WHERE platform='tiktok' AND platform_user_id=$1`, target)
	})

	c := &Crawler{pool: pool, cfg: testCrawlerConfig("test"), log: zap.NewNop()}
	_, resolveErr := c.resolveHandle(ctx, target)
	if resolveErr == nil {
		t.Fatal("expected error for null handle, got nil")
	}
}

// TestResolveHandle_MissingAccount verifies resolveHandle errors when no account row exists.
func TestResolveHandle_MissingAccount(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	c := &Crawler{pool: pool, cfg: testCrawlerConfig("test"), log: zap.NewNop()}
	_, err := c.resolveHandle(ctx, "nonexistent_uid_xyz_T11")
	if err == nil {
		t.Fatal("expected error for missing account, got nil")
	}
}

// TestBuildEnvelope verifies envelope shape matches FetchEnvelope spec.
func TestBuildEnvelope(t *testing.T) {
	env := buildEnvelope("tt_123", "content")
	if env["status"] != "ok" {
		t.Errorf("status = %v, want %q", env["status"], "ok")
	}
	prov, ok := env["provenance"].(map[string]any)
	if !ok {
		t.Fatal("provenance not a map")
	}
	if prov["source_nature"] != "scraper" {
		t.Errorf("source_nature = %v, want %q", prov["source_nature"], "scraper")
	}
	if prov["confidence"] != 0.7 {
		t.Errorf("confidence = %v, want 0.7", prov["confidence"])
	}
	req, ok := env["request"].(map[string]any)
	if !ok {
		t.Fatal("request not a map")
	}
	if req["target"] != "tt_123" {
		t.Errorf("request.target = %v, want %q", req["target"], "tt_123")
	}
	if req["scope"] != "content" {
		t.Errorf("request.scope = %v, want %q", req["scope"], "content")
	}
}

// TestHandleContent_PartialErrorFormat verifies the partial error message format.
func TestHandleContent_PartialErrorFormat(t *testing.T) {
	pool := openTestPool(t)
	writer := openTestWriter(t, pool)
	ctx := context.Background()

	sourceID := "scraper_tiktok_T11_errfmt"
	target := "tt_uid_errfmt"
	handle := "errfmt_user"

	seedSocialAccount(t, pool, target, handle)
	rowID := seedFetchRequest(t, pool, sourceID, target, "content")
	cleanupRecords(t, pool, sourceID)

	rawBytes := []byte(`{"item_list":[{"id":"ef1"}],"hasMore":1}`)
	items := makeContentItems(rawBytes, "ef1")

	// 1 page completed, then pagination fails.
	row := fetcher.FetchRequest{ID: rowID, Target: target, Scope: "content", SourceID: sourceID}

	cfg := testCrawlerConfig(sourceID)

	// Re-use via handleContent with injected crawl error logic via stubContentCrawler.
	stub := &stubClaimLoop{
		rows:  []fetcher.FetchRequest{row},
		wsURL: "ws://stub",
	}
	contentStub := &stubContentCrawler{
		items:         items,
		pagesComplete: 1,
		err:           fmt.Errorf("timeout on page 2"),
	}
	c2 := newWithDeps(stub, nil, writer, contentStub, nil, pool, cfg, zap.NewNop())
	c2.Handle(stub.rows)

	var lastError *string
	_ = pool.QueryRow(ctx,
		`SELECT last_error FROM raw.fetch_request WHERE id::text=$1`, rowID).Scan(&lastError)

	if lastError == nil {
		t.Fatal("expected non-null last_error for partial failure")
	}
	expected := "partial: landed 1 items across 1 pages, failed on page 2: timeout on page 2"
	if *lastError != expected {
		t.Errorf("last_error = %q\nwant       = %q", *lastError, expected)
	}
}

// ---- profile scope tests ----

// TestCrawler_HappyPath_ProfileScope seeds a fetch_request with scope='profile',
// runs Crawler with a stub returning a profile item, and asserts one raw.record
// with entity_kind='profile' and process_status='landed'.
func TestCrawler_HappyPath_ProfileScope(t *testing.T) {
	pool := openTestPool(t)
	writer := openTestWriter(t, pool)
	ctx := context.Background()

	sourceID := "scraper_tiktok_T12_happy"
	target := "tt_uid_profile_001"
	handle := "profile_user_001"

	seedSocialAccount(t, pool, target, handle)
	rowID := seedFetchRequest(t, pool, sourceID, target, "profile")
	cleanupRecords(t, pool, sourceID)

	rawBytes := []byte(`{"userInfo":{"user":{"id":"uid001","uniqueId":"profile_user_001","nickname":"Profile User","signature":"bio here","verified":true,"secUid":"su001"},"stats":{"followerCount":10000,"followingCount":500,"heartCount":50000,"videoCount":100}}}`)
	profileItem := &ProfileItem{
		SourceRecordID: "uid001",
		UniqueID:       "profile_user_001",
		Nickname:       "Profile User",
		Signature:      "bio here",
		Verified:       true,
		SecUID:         "su001",
		FollowerCount:  10000,
		FollowingCount: 500,
		HeartCount:     50000,
		VideoCount:     100,
		RawBytes:       rawBytes,
		FetchedAt:      time.Now().UTC(),
	}

	stub := &stubClaimLoop{
		rows:  []fetcher.FetchRequest{{ID: rowID, Target: target, Scope: "profile", SourceID: sourceID}},
		wsURL: "ws://stub:9999",
	}
	contentStub := &stubContentCrawler{}
	profileStub := &stubProfileCrawler{item: profileItem}
	c := newWithDeps(stub, nil, writer, contentStub, profileStub, pool, testCrawlerConfig(sourceID), zaptest.NewLogger(t))
	c.Handle(stub.rows)

	// Assert exactly one raw.record row with entity_kind='profile' and process_status='landed'.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM raw.record WHERE source_id=$1 AND entity_kind='profile' AND process_status='landed'`, sourceID,
	).Scan(&count); err != nil {
		t.Fatalf("query raw.record: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 profile raw.record row, got %d", count)
	}

	// Spot-check: assert the correct source_record_id was landed.
	var srcRecordID string
	if err := pool.QueryRow(ctx,
		`SELECT source_record_id FROM raw.record
		 WHERE source_id=$1 AND entity_kind='profile' AND process_status='landed'`,
		sourceID,
	).Scan(&srcRecordID); err != nil {
		t.Fatalf("query source_record_id: %v", err)
	}
	if srcRecordID != "uid001" {
		t.Errorf("source_record_id = %q, want %q", srcRecordID, "uid001")
	}

	// Assert fetch_request.status='landed'.
	var status string
	var lastError *string
	if err := pool.QueryRow(ctx,
		`SELECT status, last_error FROM raw.fetch_request WHERE id::text=$1`, rowID,
	).Scan(&status, &lastError); err != nil {
		t.Fatalf("query fetch_request: %v", err)
	}
	if status != "landed" {
		t.Errorf("status = %q, want %q", status, "landed")
	}
	if lastError != nil {
		t.Errorf("last_error = %q, want NULL", *lastError)
	}
}

// TestCrawler_ProfileScope_CrawlError verifies crawl error → status='failed'.
func TestCrawler_ProfileScope_CrawlError(t *testing.T) {
	pool := openTestPool(t)
	writer := openTestWriter(t, pool)
	ctx := context.Background()

	sourceID := "scraper_tiktok_T12_crawlerr"
	target := "tt_uid_profile_err"
	handle := "profile_err_user"

	seedSocialAccount(t, pool, target, handle)
	rowID := seedFetchRequest(t, pool, sourceID, target, "profile")

	stub := &stubClaimLoop{
		rows:  []fetcher.FetchRequest{{ID: rowID, Target: target, Scope: "profile", SourceID: sourceID}},
		wsURL: "ws://stub:9999",
	}
	contentStub := &stubContentCrawler{}
	profileStub := &stubProfileCrawler{err: fmt.Errorf("timeout waiting for /api/user/detail/ response")}
	c := newWithDeps(stub, nil, writer, contentStub, profileStub, pool, testCrawlerConfig(sourceID), zap.NewNop())
	c.Handle(stub.rows)

	var status string
	var lastError *string
	if err := pool.QueryRow(ctx,
		`SELECT status, last_error FROM raw.fetch_request WHERE id::text=$1`, rowID,
	).Scan(&status, &lastError); err != nil {
		t.Fatalf("query fetch_request: %v", err)
	}
	if status != "failed" {
		t.Errorf("status = %q, want %q", status, "failed")
	}
	if lastError == nil {
		t.Error("last_error should be non-null on profile crawl failure")
	}
}

// ---- unit tests for extractProfileItem ----

// TestExtractProfileItem_HappyPath verifies parsing a /api/user/detail/ response.
func TestExtractProfileItem_HappyPath(t *testing.T) {
	raw := []byte(`{
		"userInfo": {
			"user": {
				"id": "123456789",
				"uniqueId": "testuser",
				"nickname": "Test User",
				"signature": "this is my bio",
				"verified": true,
				"secUid": "sec_abc123"
			},
			"stats": {
				"followerCount": 100000,
				"followingCount": 200,
				"heartCount": 5000000,
				"videoCount": 300
			}
		}
	}`)

	item, err := extractProfileItem(raw)
	if err != nil {
		t.Fatalf("extractProfileItem: %v", err)
	}
	if item.SourceRecordID != "123456789" {
		t.Errorf("SourceRecordID = %q, want %q", item.SourceRecordID, "123456789")
	}
	if item.UniqueID != "testuser" {
		t.Errorf("UniqueID = %q, want %q", item.UniqueID, "testuser")
	}
	if item.Nickname != "Test User" {
		t.Errorf("Nickname = %q, want %q", item.Nickname, "Test User")
	}
	if item.Signature != "this is my bio" {
		t.Errorf("Signature = %q, want %q", item.Signature, "this is my bio")
	}
	if !item.Verified {
		t.Error("Verified should be true")
	}
	if item.SecUID != "sec_abc123" {
		t.Errorf("SecUID = %q, want %q", item.SecUID, "sec_abc123")
	}
	if item.FollowerCount != 100000 {
		t.Errorf("FollowerCount = %d, want 100000", item.FollowerCount)
	}
	if item.FollowingCount != 200 {
		t.Errorf("FollowingCount = %d, want 200", item.FollowingCount)
	}
	if item.HeartCount != 5000000 {
		t.Errorf("HeartCount = %d, want 5000000", item.HeartCount)
	}
	if item.VideoCount != 300 {
		t.Errorf("VideoCount = %d, want 300", item.VideoCount)
	}
	if string(item.RawBytes) != string(raw) {
		t.Error("RawBytes should equal the full response body")
	}
}

// TestExtractProfileItem_MissingUserInfo verifies error on missing userInfo.
func TestExtractProfileItem_MissingUserInfo(t *testing.T) {
	raw := []byte(`{"status_code": 0}`)
	_, err := extractProfileItem(raw)
	if err == nil {
		t.Fatal("expected error for missing userInfo, got nil")
	}
}

// TestExtractProfileItem_EmptyID verifies error when user.id is empty.
func TestExtractProfileItem_EmptyID(t *testing.T) {
	raw := []byte(`{"userInfo":{"user":{"id":"","uniqueId":"x"},"stats":{}}}`)
	_, err := extractProfileItem(raw)
	if err == nil {
		t.Fatal("expected error for empty user.id, got nil")
	}
}

// TestExtractProfileItem_UnverifiedUser verifies verified=false is handled.
func TestExtractProfileItem_UnverifiedUser(t *testing.T) {
	raw := []byte(`{"userInfo":{"user":{"id":"uid_unverified","uniqueId":"u","nickname":"N","signature":"S","verified":false,"secUid":"su"},"stats":{"followerCount":1,"followingCount":1,"heartCount":1,"videoCount":1}}}`)
	item, err := extractProfileItem(raw)
	if err != nil {
		t.Fatalf("extractProfileItem: %v", err)
	}
	if item.Verified {
		t.Error("Verified should be false")
	}
	if item.SourceRecordID != "uid_unverified" {
		t.Errorf("SourceRecordID = %q, want %q", item.SourceRecordID, "uid_unverified")
	}
}
