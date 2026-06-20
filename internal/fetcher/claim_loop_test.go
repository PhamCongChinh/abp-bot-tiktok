package fetcher

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest"

	"abp-bot-tiktok/pkg/config"
	"abp-bot-tiktok/pkg/gologin"
)

// stubGoLogin is a test double that records Start/Stop calls.
type stubGoLogin struct {
	mu       sync.Mutex
	started  []string
	stopped  []string
	wsURL    string
	startErr error
}

func (s *stubGoLogin) Start(profileID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startErr != nil {
		return "", s.startErr
	}
	s.started = append(s.started, profileID)
	return s.wsURL, nil
}

func (s *stubGoLogin) Stop(profileID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = append(s.stopped, profileID)
	return nil
}

func testLogger(t *testing.T) *zap.Logger {
	t.Helper()
	return zaptest.NewLogger(t, zaptest.Level(zapcore.WarnLevel))
}

// openTestPool connects to the test Postgres instance. Returns nil and skips
// the test if POSTGRES_TEST_DSN is not set.
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
	t.Cleanup(pool.Close)
	return pool
}

// seedFetchRequest inserts a single raw.fetch_request row with status='queued'
// and returns its UUID as a string.
func seedFetchRequest(t *testing.T, pool *pgxpool.Pool, sourceID string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO raw.fetch_request
			(source_id, target, scope, status)
		VALUES ($1, 'test_target', 'content', 'queued')
		RETURNING id::text
	`, sourceID).Scan(&id)
	if err != nil {
		t.Fatalf("seed fetch_request: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), //nolint:errcheck
			`DELETE FROM raw.fetch_request WHERE source_id = $1 AND target = 'test_target'`, sourceID)
	})
	return id
}

func testConfig(sourceID string) *config.Config {
	return &config.Config{
		SourceID:            sourceID,
		ClaimChunk:          5,
		ClaimPollIntervalMS: 100,
		TikTokProfileIDs:    []string{"profile-1", "profile-2"},
	}
}

// TestPoll_ClaimAndSkipLocked verifies that Poll() claims a queued row and
// that a concurrent second Poll() call (SKIP LOCKED) does not see it.
func TestPoll_ClaimAndSkipLocked(t *testing.T) {
	pool := openTestPool(t)
	sourceID := "scraper_tiktok_test_T9"
	id := seedFetchRequest(t, pool, sourceID)

	stub := &stubGoLogin{wsURL: "ws://localhost:9222"}
	cfg := testConfig(sourceID)
	log := testLogger(t)

	cl, err := newWithPool(pool, cfg, stub, log)
	if err != nil {
		t.Fatalf("newWithPool: %v", err)
	}
	// Seed currentWsURL so Init is not needed for Poll tests.
	cl.currentWsURL = stub.wsURL

	// First Poll should claim the row.
	items, err := cl.Poll()
	if err != nil {
		t.Fatalf("first Poll: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].ID != id {
		t.Errorf("expected id %s, got %s", id, items[0].ID)
	}
	if items[0].Scope != "content" {
		t.Errorf("expected scope content, got %s", items[0].Scope)
	}

	// Verify the row is now 'claimed' in the DB.
	var status string
	err = pool.QueryRow(context.Background(),
		`SELECT status FROM raw.fetch_request WHERE id::text = $1`, id).Scan(&status)
	if err != nil {
		t.Fatalf("verify status: %v", err)
	}
	if status != "claimed" {
		t.Errorf("expected status=claimed, got %s", status)
	}

	// Second Poll (SKIP LOCKED) must not see the already-claimed row.
	items2, err := cl.Poll()
	if err != nil {
		t.Fatalf("second Poll: %v", err)
	}
	if len(items2) != 0 {
		t.Errorf("expected 0 items on second Poll, got %d", len(items2))
	}
}

// TestPoll_ProfileRotation verifies that a non-empty Poll triggers rotation.
func TestPoll_ProfileRotation(t *testing.T) {
	pool := openTestPool(t)
	sourceID := "scraper_tiktok_test_T9_rot"
	seedFetchRequest(t, pool, sourceID)

	stub := &stubGoLogin{wsURL: "ws://localhost:9222"}
	cfg := testConfig(sourceID)
	log := testLogger(t)

	cl, err := newWithPool(pool, cfg, stub, log)
	if err != nil {
		t.Fatalf("newWithPool: %v", err)
	}
	cl.currentWsURL = stub.wsURL
	cl.currentIdx = 0

	_, err = cl.Poll()
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}

	// After a non-empty poll, rotation must have stopped profile-1 and started profile-2.
	stub.mu.Lock()
	stopped := append([]string(nil), stub.stopped...)
	started := append([]string(nil), stub.started...)
	stub.mu.Unlock()

	if len(stopped) != 1 || stopped[0] != "profile-1" {
		t.Errorf("expected profile-1 stopped, got %v", stopped)
	}
	if len(started) != 1 || started[0] != "profile-2" {
		t.Errorf("expected profile-2 started, got %v", started)
	}
	if cl.currentIdx != 1 {
		t.Errorf("expected currentIdx=1, got %d", cl.currentIdx)
	}
}

// TestPoll_EmptyQueue verifies Poll returns empty slice without error when
// there are no queued rows for the given source_id.
func TestPoll_EmptyQueue(t *testing.T) {
	pool := openTestPool(t)
	sourceID := "scraper_tiktok_test_T9_empty"

	stub := &stubGoLogin{wsURL: "ws://localhost:9222"}
	cfg := testConfig(sourceID)
	log := testLogger(t)

	cl, err := newWithPool(pool, cfg, stub, log)
	if err != nil {
		t.Fatalf("newWithPool: %v", err)
	}
	cl.currentWsURL = stub.wsURL

	items, err := cl.Poll()
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected empty result, got %d items", len(items))
	}
}

// TestInit verifies that Init starts the first profile and stores the wsUrl.
func TestInit(t *testing.T) {
	stub := &stubGoLogin{wsURL: "ws://gologin:9222"}
	cfg := testConfig("scraper_tiktok")
	log := zap.NewNop()

	// newWithPool with nil pool is safe for Init-only tests.
	cl := &ClaimLoop{
		goLogin:    stub,
		profileIDs: cfg.TikTokProfileIDs,
		cfg:        cfg,
		log:        log,
	}

	if err := cl.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if cl.currentWsURL != "ws://gologin:9222" {
		t.Errorf("expected wsUrl set, got %q", cl.currentWsURL)
	}
	if cl.currentIdx != 0 {
		t.Errorf("expected currentIdx=0, got %d", cl.currentIdx)
	}

	stub.mu.Lock()
	started := append([]string(nil), stub.started...)
	stub.mu.Unlock()

	if len(started) != 1 || started[0] != "profile-1" {
		t.Errorf("expected profile-1 started, got %v", started)
	}
}

// TestInit_Error verifies Init propagates GoLogin Start errors.
func TestInit_Error(t *testing.T) {
	stub := &stubGoLogin{startErr: fmt.Errorf("connection refused")}
	cfg := testConfig("scraper_tiktok")
	cl := &ClaimLoop{
		goLogin:    stub,
		profileIDs: cfg.TikTokProfileIDs,
		cfg:        cfg,
		log:        zap.NewNop(),
	}

	if err := cl.Init(); err == nil {
		t.Fatal("expected error from Init, got nil")
	}
}

// TestCurrentWsURL verifies the getter returns the stored URL.
func TestCurrentWsURL(t *testing.T) {
	cl := &ClaimLoop{currentWsURL: "ws://test:1234"}
	if got := cl.CurrentWsURL(); got != "ws://test:1234" {
		t.Errorf("expected ws://test:1234, got %q", got)
	}
}

// --- GoLogin HTTP client unit tests ---

// TestGoLoginClient_Start verifies Start parses wsUrl from a mocked server.
func TestGoLoginClient_Start(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/start/prof-abc" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"wsUrl":"ws://orbita:9222/devtools/browser/abc"}`)
	}))
	defer srv.Close()

	client := gologin.New(srv.URL)
	wsURL, err := client.Start("prof-abc")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if wsURL != "ws://orbita:9222/devtools/browser/abc" {
		t.Errorf("unexpected wsURL: %q", wsURL)
	}
}

// TestGoLoginClient_Start_HTTPError verifies Start returns an error on non-200.
func TestGoLoginClient_Start_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := gologin.New(srv.URL)
	_, err := client.Start("prof-x")
	if err == nil {
		t.Fatal("expected error from Start on 500, got nil")
	}
}

// TestGoLoginClient_Start_EmptyWsUrl verifies Start errors on empty wsUrl.
func TestGoLoginClient_Start_EmptyWsUrl(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"wsUrl":""}`)
	}))
	defer srv.Close()

	client := gologin.New(srv.URL)
	_, err := client.Start("prof-y")
	if err == nil {
		t.Fatal("expected error on empty wsUrl, got nil")
	}
}

// TestGoLoginClient_Stop verifies Stop calls the correct endpoint.
func TestGoLoginClient_Stop(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stop/prof-abc" {
			called = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := gologin.New(srv.URL)
	if err := client.Stop("prof-abc"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !called {
		t.Error("expected /stop/prof-abc to be called")
	}
}

// TestGoLoginClient_Stop_HTTPError verifies Stop returns an error on non-200.
func TestGoLoginClient_Stop_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadGateway)
	}))
	defer srv.Close()

	client := gologin.New(srv.URL)
	if err := client.Stop("prof-z"); err == nil {
		t.Fatal("expected error from Stop on 502, got nil")
	}
}
