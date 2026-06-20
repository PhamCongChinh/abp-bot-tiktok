# ABP Bot TikTok

TikTok crawler bot written in Go with Playwright. Part of the self-hosted crawler integration — connects to the shared GoLogin launcher sidecar, claims `fetch_request` rows from Postgres, and lands results into MinIO + Postgres.

## Architecture

This repo depends on infrastructure services provided by `instagram-crawler`:

- **`gologin-launcher`** — GoLogin sidecar (Chrome profile manager). TikTok crawler claims profiles from it after every batch.
- **`postgres`** — Shared Postgres instance. The `raw.fetch_request` claim loop reads from this.
- **`minio`** — Shared MinIO instance. Landing results are written here.
- **`crawler-net`** — Shared Docker network. All services must be on this network.

## Quick start

> **Both repos must be cloned and the IG stack must be started before running the TikTok stack.**

```bash
# 1. Start the instagram-crawler stack first (creates the shared network + services)
cd ../instagram-crawler
docker compose up -d

# 2. Copy the example env and fill in your values
cd ../abp-bot-tiktok
cp .env.example .env
# Edit .env — set TIKTOK_PROFILE_IDS, POSTGRES_DSN, MINIO_* values

# 3. Start the TikTok crawler
docker compose up -d
```

## Structure

```
├── cmd/
│   └── main.go               # Entry point (full wiring in T11)
├── internal/
│   ├── crawler/              # TikTok search scraping logic (dormant — wired in T11)
│   ├── models/               # Data models
│   ├── parser/               # TikTok post parser
│   └── utils/                # Browser utilities (scroll, delay, mouse)
└── pkg/
    ├── config/               # Config loader
    └── logger/               # Zap logger
```

## Configuration

| Variable | Default | Description |
|---|---|---|
| `LOG_LEVEL` | `info` | Log level (`debug`, `info`, `warn`, `error`) |
| `DEBUG` | `false` | Enable debug mode |
| `GOLOGIN_LAUNCHER_URL` | — | URL of the GoLogin launcher sidecar (from `instagram-crawler`) |
| `TIKTOK_PROFILE_IDS` | — | Comma-separated GoLogin profile IDs to rotate through |
| `POSTGRES_DSN` | — | Postgres connection string |
| `MINIO_ENDPOINT` | — | MinIO endpoint |
| `MINIO_ACCESS_KEY` | — | MinIO access key |
| `MINIO_SECRET_KEY` | — | MinIO secret key |
| `MINIO_BUCKET` | `raw` | MinIO bucket for landing results |
| `SOURCE_ID` | `scraper_tiktok` | Source ID written to `raw.fetch_request` claim |
| `CLAIM_CHUNK` | `5` | Number of rows to claim per poll |
| `CLAIM_POLL_INTERVAL_MS` | `10000` | Poll interval when queue is empty (ms) |
| `TIKTOK_CONTENT_PAGE_CAP` | `10` | Max pages to scroll per content item |

## Development

```bash
# Install dependencies
go mod download

# Install Playwright browsers (first time only)
go run github.com/playwright-community/playwright-go/cmd/playwright@latest install --with-deps chromium

# Build
go build ./...

# Lint (must pass before push)
golangci-lint run
```
