# ABP Bot TikTok

TikTok crawler bot viết bằng Go với Playwright, hỗ trợ lưu MongoDB và JSON backup.

## Cấu trúc

```
├── cmd/
│   └── main.go           # Entry point chính
├── internal/
│   ├── crawler/          # Logic crawl TikTok
│   ├── models/           # Data models
│   ├── repository/       # MongoDB repositories
│   ├── scheduler/        # Cron scheduler
│   └── utils/            # Utilities (scroll, delay, etc.)
├── pkg/
│   ├── config/           # Config loader
│   ├── database/         # MongoDB client
│   └── logger/           # Zap logger
└── data/                 # JSON backup files
```

## Cài đặt

### 1. Cài Go (>= 1.21)
```bash
# Kiểm tra
go version
```

### 2. Cài Playwright browsers
```bash
go run github.com/playwright-community/playwright-go/cmd/playwright@latest install --with-deps chromium
```

### 3. Cài MongoDB
- Local: https://www.mongodb.com/try/download/community
- Hoặc dùng MongoDB Atlas (cloud)

### 4. Clone & Install dependencies
```bash
cd abp-bot-tiktok
go mod download
```

## Cấu hình

### 1. Tạo `.env`
```bash
cp .env.example .env
```

Sửa `.env`:
```env
LOG_LEVEL=info
DEBUG=true
BOT_NAME=bot-test

# MongoDB
MONGO_URI=mongodb://localhost:27017
MONGO_DB=tiktok_crawler

# Chrome path
CHROME_PATH=C:/Program Files/Google/Chrome/Application/chrome.exe

# Cron (production)
CRON_SCHEDULE=0 */6 * * *

# Output
OUTPUT_DIR=./data

# Timing
BATCH_MIN=5
BATCH_MAX=10
SLEEP_MIN_KEYWORD=60
SLEEP_MAX_KEYWORD=120
REST_MIN_SESSION=600
REST_MAX_SESSION=900
```

### 2. Tạo `profiles.json` (nếu cần multi-profile)
Không cần nữa - bot crawl TikTok public search không cần login.

## Sử dụng

### Chạy crawler (DEBUG mode)
```bash
go run cmd/main.go
```

### Chạy production (với cron)
```bash
# Sửa .env: DEBUG=false
go build -o bot.exe cmd/main.go
./bot.exe
```

## MongoDB Collections

### `tiktok_videos`
```javascript
{
  keyword: "Xã Xuân Giang",
  video_id: "7123456789",
  description: "...",
  pub_time: 1714089600,
  unique_id: "username",
  auth_id: "123456",
  auth_name: "Display Name",
  comments: 100,
  shares: 50,
  reactions: 1000,
  favors: 200,
  views: 10000,
  crawled_at: ISODate("2026-04-26T..."),
  updated_at: ISODate("2026-04-26T...")
}
```

### `keyword`
```javascript
{
  _id: ObjectId("..."),
  keyword: "Xã Xuân Giang",
  org_id: 1,
  active: true
}
```

### `tiktok_bot_configs`
```javascript
{
  _id: ObjectId("..."),
  bot_name: "bot-test",
  bot_type: "video",
  org_id: ["1", "2"],
  sleep: 360,  // minutes
  active: true,
  created_at: ISODate("..."),
  updated_at: ISODate("...")
}
```

## Tính năng

- ✅ Multi-profile support
- ✅ Intercept TikTok search API
- ✅ Human-like behavior (scroll, mouse move, random view)
- ✅ MongoDB upsert (tránh duplicate)
- ✅ JSON backup
- ✅ Batch processing với random sleep
- ✅ Cron scheduler
- ✅ Structured logging (Zap)

## Anti-ban

Code đã có:
- Random sleep giữa keywords (60-120s)
- Random rest giữa sessions (600-900s)
- Human scroll simulation
- Random mouse movement
- Random video viewing

Khuyến nghị thêm:
- Chỉ chạy 7h-23h (tránh 2h-6h sáng)
- Giới hạn ~50-100 keywords/ngày
- Dùng profile Chrome thật (có lịch sử)
- Rotate IP nếu crawl nhiều

## License

MIT
