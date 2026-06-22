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

# Everything — infra + both crawlers:
docker compose up

# Instagram only (skip TikTok crawler):
docker compose up --scale tiktok-crawler=0
```

See `kolsquare/.env.runtime` for required secrets (GoLogin token, TIKTOK_PROFILE_IDS, etc.).

## GoLogin profile setup

### Getting the real profile UUID

`TIKTOK_PROFILE_IDS` must contain the **profile UUID** (a 24-character hex string), not the
profile display name. The display name (e.g. `tiktok-profile-1`) is shown in the GoLogin UI
but is not accepted by the API.

To find the real UUID:

1. Open [app.gologin.com](https://app.gologin.com) → Profiles
2. Open the profile you want to use — the UUID appears in the URL:
   `https://app.gologin.com/.../<24-char-hex-id>`
3. Alternatively, list all profiles via the API:
   ```bash
   node -e "
   const { GoLogin } = require('gologin');
   const gl = new GoLogin({ token: 'YOUR_TOKEN' });
   gl.profiles().then(r => r.profiles.forEach(p => console.log(p.id, '|', p.name)));
   "
   ```
4. Set in `.env.runtime`:
   ```
   TIKTOK_PROFILE_IDS=6a36d3a62d2dd901902ca304
   ```

For multiple profiles, use a comma-separated list — the crawler rotates through them.

### gologin-launcher sidecar requirements

The TikTok crawler depends on the `gologin-launcher` sidecar (from `instagram-crawler/gologin-launcher`).
For the sidecar to work correctly in Docker, it needs:

| Requirement | Why |
|---|---|
| `gologin` npm **≥ 2.2.9** | Older versions don't call the `/profile-params-for-orbita-token` API endpoint and never write `orbita.config` — Orbita exits with code 403. |
| Chromium system libraries in the container | `node:20-slim` ships without them; without `libglib2.0-0`, `libcurl3-gnutls`, etc., Orbita crashes immediately on `execFile`. |
| `--no-sandbox` flag | Orbita refuses to run as root (the Docker default) without this flag. |
| `--headless=new` in `args` | GoLogin ≥ 2.2.9 removed the `headless:` constructor option; the flag must be passed via `args: ['--headless=new', ...]`. |
| `--disable-dev-shm-usage` | Docker's default 64 MB `/dev/shm` is too small for Chromium; this flag redirects shared memory to `/tmp`. |
| `skipOrbitaHashChecking: true` | GoLogin's CDN sometimes serves a tarball whose SHA doesn't match the separately-downloaded `hashfile.txt`; this skips the check to unblock startup. |

All of these are already applied in `instagram-crawler/gologin-launcher/` — no manual changes needed unless you rebuild from scratch.

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

## Troubleshooting

### `GoLogin is not a constructor`

**Symptom:** gologin-launcher logs `TypeError: GoLogin is not a constructor` on every `/start/` call.

**Cause:** The `gologin` package uses named exports. `require('gologin')` returns the module
object, not the constructor.

**Fix:** Use destructuring:
```js
// Wrong
const GoLogin = require('gologin');
// Correct
const { GoLogin } = require('gologin');
```

---

### `Error in sum matching. Please run script again.`

**Symptom:** gologin-launcher exits after downloading Orbita with this error.

**Cause:** GoLogin's CDN serves the Orbita tarball and `hashfile.txt` independently. They
occasionally diverge (version skew), causing the SHA256 comparison to fail.

**Fix:** Pass `skipOrbitaHashChecking: true` to the `GoLogin` constructor. The download itself
is valid; only the remote hash file is stale.

---

### `INVALID TOKEN OR PROFILE NOT FOUND` (HTTP 500 from gologin-launcher)

**Symptom:** TikTok crawler logs `unexpected status 500: {"error":"...INVALID TOKEN OR PROFILE NOT FOUND"}`.

**Cause:** `TIKTOK_PROFILE_IDS` contains the display name (e.g. `tiktok-profile-1`) instead of
the actual GoLogin profile UUID.

**Fix:** Set `TIKTOK_PROFILE_IDS` to the 24-character hex UUID. See [GoLogin profile setup](#gologin-profile-setup) above.

---

### `libglib-2.0.so.0: cannot open shared object file` (or `libcurl-gnutls.so.4`)

**Symptom:** Orbita crashes immediately with a shared-library error when gologin-launcher calls `execFile`.

**Cause:** `node:20-slim` (Debian Bookworm) ships without the Chromium runtime dependencies.

**Fix:** Add these packages to the gologin-launcher `Dockerfile`:
```dockerfile
RUN apt-get update && apt-get install -y --no-install-recommends \
    libglib2.0-0 libnss3 libnspr4 libatk1.0-0 libatk-bridge2.0-0 \
    libcups2 libdrm2 libdbus-1-3 libexpat1 libxcb1 libxkbcommon0 \
    libx11-6 libxcomposite1 libxdamage1 libxext6 libxfixes3 libxrandr2 \
    libgbm1 libpango-1.0-0 libcairo2 libasound2 libatspi2.0-0 \
    fonts-liberation ca-certificates libcurl3-gnutls \
  && rm -rf /var/lib/apt/lists/*
```

---

### `orbita.config not found or not readable in user data dir (exit code 403)`

**Symptom:** Orbita starts (or nearly starts) then exits with this message in `chrome_debug.log`.

**Cause:** `gologin` < 2.2.9 never calls the `/browser/features/<id>/profile-params-for-orbita-token`
API endpoint, so `orbita.config` is never written to the profile directory. Orbita requires this
file for license verification.

**Fix:** Upgrade `gologin` to **≥ 2.2.9** in `gologin-launcher/package.json`:
```json
"gologin": "2.2.9"
```

---

### `Running as root without --no-sandbox is not supported`

**Symptom:** `chrome_debug.log` contains this error; CDP port never opens.

**Cause:** Docker containers run as root by default. Chrome/Orbita refuses to start in this
configuration without the sandbox explicitly disabled.

**Fix:** Pass the flag via the `args` option:
```js
new GoLogin({ ..., args: ['--no-sandbox', '--disable-dev-shm-usage', '--headless=new'] })
```

Note: GoLogin ≥ 2.2.9 removed the `headless: true` constructor option. The flag must be in `args`.

---

### `Missing X server or $DISPLAY` / `The platform failed to initialize`

**Symptom:** `chrome_debug.log` shows this error; follows the root+sandbox fix but still crashes.

**Cause:** GoLogin ≥ 2.2.9 no longer translates `headless: true` into `--headless` on the
command line. Without the flag, Orbita tries to open a display.

**Fix:** Include `--headless=new` in the `args` array (see above).

---

### `context deadline exceeded` on `/start/` call

**Symptom:** TikTok crawler logs `Client.Timeout exceeded while awaiting headers` from `http://gologin-launcher:4000/start/<id>`.

**Cause:** Orbita is still downloading/extracting (~400 MB tarball) when the crawler's first
`/start/` request arrives, and the request times out before Orbita is ready.

**Behaviour:** This is transient — the crawler restarts (via `restart: on-failure`) and retries.
Once Orbita is installed the subsequent `/start/` calls succeed in ~20 s.

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
