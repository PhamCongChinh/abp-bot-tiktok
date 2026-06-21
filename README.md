# ABP Bot TikTok

TikTok crawler bot written in Go with Playwright. Part of the self-hosted crawler integration — connects to the shared GoLogin launcher sidecar, claims `fetch_request` rows from Postgres, and lands results into MinIO + Postgres.

## Architecture

Shared infrastructure (postgres, minio, gologin-launcher, crawler-net) is defined in the
workspace-level `docker-compose.yml` (kolsquare root). There is no per-repo compose file.

- **`gologin-launcher`** — GoLogin sidecar (Chrome profile manager). Profile rotation happens after every non-empty poll batch.
- **`postgres`** — Shared Postgres. `raw.fetch_request` claim loop reads from here.
- **`minio`** — Shared MinIO. Landing payloads are written here.
- **`crawler-net`** — Shared Docker network.

## Database dependency

**Requires kol-data-platform Alembic migration `0003` or later.**

Schema is owned by `kol-data-platform` — this crawler does not run its own migrations.
The service will refuse to start and print a clear error if the DB is behind.

To apply migrations:
```bash
cd kol-data-platform
alembic upgrade head   # or: make migrate
```

## Quick start

```bash
# From the kolsquare workspace root:

# Infra + TikTok crawler:
docker compose --profile tiktok up

# Both crawlers:
docker compose --profile instagram --profile tiktok up
```

See `kolsquare/.env.runtime` for required secrets (GoLogin token, TIKTOK_PROFILE_IDS, etc.).

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
