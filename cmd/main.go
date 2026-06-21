package main

import (
	"abp-bot-tiktok/internal/crawler"
	"abp-bot-tiktok/internal/fetcher"
	"abp-bot-tiktok/internal/landing"
	"abp-bot-tiktok/pkg/config"
	"abp-bot-tiktok/pkg/gologin"
	"abp-bot-tiktok/pkg/logger"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/playwright-community/playwright-go"
	"go.uber.org/zap"
)

func main() {
	cfg := config.Load()
	log := logger.New(cfg.LogLevel)
	defer func() { _ = log.Sync() }()

	log.Info("abp-bot-tiktok starting")

	if err := validateConfig(cfg, log); err != nil {
		log.Fatal("invalid config", zap.Error(err))
	}

	// Postgres pool.
	pool, err := pgxpool.New(context.Background(), cfg.PostgresDSN)
	if err != nil {
		log.Fatal("pgx pool", zap.Error(err))
	}
	defer pool.Close()

	// MinIO landing writer.
	minioEndpoint := strings.TrimPrefix(cfg.MinIOEndpoint, "http://")
	minioEndpoint = strings.TrimPrefix(minioEndpoint, "https://")
	useSSL := strings.HasPrefix(cfg.MinIOEndpoint, "https://")
	writer, err := landing.New(pool, minioEndpoint, cfg.MinIOAccessKey, cfg.MinIOSecretKey, cfg.MinIOBucket, useSSL)
	if err != nil {
		log.Fatal("landing writer", zap.Error(err))
	}

	// GoLogin client + ClaimLoop.
	glClient := gologin.New(cfg.GoLoginLauncherURL)
	claimLoop, err := fetcher.New(cfg, glClient, log)
	if err != nil {
		log.Fatal("claim loop", zap.Error(err))
	}

	// Playwright.
	pw, err := playwright.Run()
	if err != nil {
		log.Fatal("playwright run", zap.Error(err))
	}
	defer func() { _ = pw.Stop() }()

	// Crawler.
	c := crawler.New(claimLoop, pw, writer, pool, cfg, log)

	log.Info("claim loop started",
		zap.String("source_id", cfg.SourceID),
		zap.Int("chunk", cfg.ClaimChunk),
		zap.Int("page_cap", cfg.TikTokContentPageCap),
	)

	claimLoop.Run(c.Handle)
}

func validateConfig(cfg *config.Config, log *zap.Logger) error {
	missing := []string{}
	if cfg.PostgresDSN == "" {
		missing = append(missing, "POSTGRES_DSN")
	}
	if cfg.GoLoginLauncherURL == "" {
		missing = append(missing, "GOLOGIN_LAUNCHER_URL")
	}
	if cfg.MinIOEndpoint == "" {
		missing = append(missing, "MINIO_ENDPOINT")
	}
	if cfg.MinIOBucket == "" {
		missing = append(missing, "MINIO_BUCKET")
	}
	if len(cfg.TikTokProfileIDs) == 0 {
		missing = append(missing, "TIKTOK_PROFILE_IDS")
	}
	if len(missing) > 0 {
		for _, m := range missing {
			log.Error("missing required env var", zap.String("var", m))
		}
		fmt.Fprintf(os.Stderr, "missing required env vars: %s\n", strings.Join(missing, ", "))
		return fmt.Errorf("missing env vars: %s", strings.Join(missing, ", "))
	}
	return nil
}
