package fetcher

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"abp-bot-tiktok/pkg/config"
)

// GoLoginClient is the interface for managing GoLogin browser profiles.
// The real implementation is *gologin.Client; tests inject a stub.
type GoLoginClient interface {
	Start(profileID string) (string, error)
	Stop(profileID string) error
}

// FetchRequest is a row claimed from raw.fetch_request.
type FetchRequest struct {
	ID       string // UUID as hyphenated text
	Target   string // platform_user_id as enqueued by the refresh DAG
	Scope    string // "profile" or "content"
	SourceID string
}

// ClaimLoop polls raw.fetch_request for queued TikTok rows and owns the
// GoLogin session lifecycle. Session rotation occurs after every non-empty
// Poll() call — TikTok's fingerprint checks are session-length sensitive.
type ClaimLoop struct {
	pool         *pgxpool.Pool
	goLogin      GoLoginClient
	profileIDs   []string
	currentIdx   int
	currentWsURL string
	cfg          *config.Config
	log          *zap.Logger
}

// New constructs a ClaimLoop with a pgx/v5 connection pool.
func New(cfg *config.Config, goLogin GoLoginClient, log *zap.Logger) (*ClaimLoop, error) {
	if len(cfg.TikTokProfileIDs) == 0 {
		return nil, fmt.Errorf("TIKTOK_PROFILE_IDS must not be empty")
	}

	pool, err := pgxpool.New(context.Background(), cfg.PostgresDSN)
	if err != nil {
		return nil, fmt.Errorf("pgx pool: %w", err)
	}

	return &ClaimLoop{
		pool:       pool,
		goLogin:    goLogin,
		profileIDs: cfg.TikTokProfileIDs,
		cfg:        cfg,
		log:        log,
	}, nil
}

// newWithPool is used by tests to inject a pre-built pool directly.
func newWithPool(pool *pgxpool.Pool, cfg *config.Config, goLogin GoLoginClient, log *zap.Logger) (*ClaimLoop, error) {
	if len(cfg.TikTokProfileIDs) == 0 {
		return nil, fmt.Errorf("TIKTOK_PROFILE_IDS must not be empty")
	}
	return &ClaimLoop{
		pool:       pool,
		goLogin:    goLogin,
		profileIDs: cfg.TikTokProfileIDs,
		cfg:        cfg,
		log:        log,
	}, nil
}

// Init starts the GoLogin session for the first profile. Must be called once
// before Poll() or CurrentWsURL().
func (cl *ClaimLoop) Init() error {
	wsURL, err := cl.goLogin.Start(cl.profileIDs[0])
	if err != nil {
		return fmt.Errorf("init: start profile %s: %w", cl.profileIDs[0], err)
	}
	cl.currentWsURL = wsURL
	cl.currentIdx = 0
	cl.log.Info("gologin session started",
		zap.String("profile", cl.profileIDs[0]),
		zap.String("wsUrl", wsURL),
	)
	return nil
}

// CurrentWsURL returns the WebSocket URL of the currently active GoLogin session.
// Crawler calls this once per batch (after Poll() returns) to connect Playwright.
func (cl *ClaimLoop) CurrentWsURL() string {
	return cl.currentWsURL
}

// Poll claims up to ClaimChunk queued rows from raw.fetch_request within a
// single transaction (FOR UPDATE SKIP LOCKED). After a non-empty result the
// GoLogin profile is rotated.
func (cl *ClaimLoop) Poll() ([]FetchRequest, error) {
	ctx := context.Background()
	hostname, _ := os.Hostname()

	tx, err := cl.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("poll: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	rows, err := tx.Query(ctx, `
		SELECT id::text, target, scope, source_id
		FROM raw.fetch_request
		WHERE source_id = $1 AND status = 'queued'
		FOR UPDATE SKIP LOCKED
		LIMIT $2
	`, cl.cfg.SourceID, cl.cfg.ClaimChunk)
	if err != nil {
		return nil, fmt.Errorf("poll: select: %w", err)
	}

	var items []FetchRequest
	for rows.Next() {
		var fr FetchRequest
		if err := rows.Scan(&fr.ID, &fr.Target, &fr.Scope, &fr.SourceID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("poll: scan: %w", err)
		}
		items = append(items, fr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("poll: rows: %w", err)
	}

	if len(items) > 0 {
		ids := make([]string, len(items))
		for i, fr := range items {
			ids[i] = fr.ID
		}
		_, err = tx.Exec(ctx, `
			UPDATE raw.fetch_request
			SET status = 'claimed',
			    claimed_at = NOW(),
			    claimed_by = $1
			WHERE id::text = ANY($2)
		`, hostname, ids)
		if err != nil {
			return nil, fmt.Errorf("poll: update: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("poll: commit: %w", err)
	}

	if len(items) > 0 {
		if err := cl.rotateProfile(); err != nil {
			cl.log.Warn("profile rotation failed", zap.Error(err))
		}
	}

	return items, nil
}

// rotateProfile stops the outgoing profile and starts the next one round-robin.
func (cl *ClaimLoop) rotateProfile() error {
	outgoing := cl.profileIDs[cl.currentIdx]
	cl.currentIdx = (cl.currentIdx + 1) % len(cl.profileIDs)
	next := cl.profileIDs[cl.currentIdx]

	if err := cl.goLogin.Stop(outgoing); err != nil {
		cl.log.Warn("stop profile failed",
			zap.String("profile", outgoing),
			zap.Error(err),
		)
	}

	wsURL, err := cl.goLogin.Start(next)
	if err != nil {
		return fmt.Errorf("rotate: start %s: %w", next, err)
	}
	cl.currentWsURL = wsURL
	cl.log.Info("profile rotated",
		zap.String("from", outgoing),
		zap.String("to", next),
		zap.String("wsUrl", wsURL),
	)
	return nil
}

// Run is the main claim loop. It calls Init() once, then polls indefinitely,
// calling dispatch for each non-empty batch. Sleeps ClaimPollIntervalMS on
// empty results.
func (cl *ClaimLoop) Run(dispatch func([]FetchRequest)) {
	if err := cl.Init(); err != nil {
		cl.log.Fatal("claim loop init failed", zap.Error(err))
	}

	for {
		items, err := cl.Poll()
		if err != nil {
			cl.log.Error("poll failed", zap.Error(err))
			time.Sleep(time.Duration(cl.cfg.ClaimPollIntervalMS) * time.Millisecond)
			continue
		}

		if len(items) == 0 {
			time.Sleep(time.Duration(cl.cfg.ClaimPollIntervalMS) * time.Millisecond)
			continue
		}

		dispatch(items)
	}
}
