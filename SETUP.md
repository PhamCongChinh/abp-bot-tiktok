# Setup Guide

## Bước 1: Cài đặt Go
- Download: https://go.dev/dl/
- Version >= 1.21

## Bước 2: Clone project
```bash
git clone <repo-url>
cd abp-bot-tiktok
```

## Bước 3: Cài dependencies
```bash
go mod download
```

## Bước 4: Cài Playwright driver
**Windows:**
```bash
install.bat
```

**Linux/Mac:**
```bash
go run github.com/playwright-community/playwright-go/cmd/playwright@v0.5700.1 install chromium
```

## Bước 5: Cấu hình .env

Copy file mẫu:
```bash
cp .env.example .env
```

Sửa `.env`:
```env
# MongoDB
MONGO_URI=mongodb://user:pass@host:port/?authSource=admin
MONGO_DB=abp_warehouse
ORG_ID=2

# GPM (bắt buộc)
GPM_API=http://127.0.0.1:19995/api/v3
PROFILE_ID=<your-real-profile-id>
```

### Lấy GPM Profile ID:
1. Mở GPM Login
2. Click vào profile muốn dùng
3. Copy Profile ID từ URL hoặc profile settings

### Kiểm tra GPM đang chạy:
```bash
curl http://127.0.0.1:19995/api/v3/profiles
```

Nếu lỗi → Mở GPM Login trước

## Bước 6: Chạy bot

**DEBUG mode (chạy 1 lần):**
```bash
go run cmd/main.go
```

**Production (build binary):**
```bash
go build -o bot.exe cmd/main.go
./bot.exe
```

## Troubleshooting

### Lỗi: "please install the driver (v1.57.0) first"
→ Chạy lại `install.bat` hoặc:
```bash
go run github.com/playwright-community/playwright-go/cmd/playwright@v0.5700.1 install chromium
```

### Lỗi: "No connection could be made... 127.0.0.1:19995"
→ GPM chưa chạy. Mở GPM Login trước.

### Lỗi: "failed to start profile"
→ Kiểm tra `PROFILE_ID` đúng chưa. Không dùng `your-profile-id-here`.

### Lỗi: "No keywords found for org_id"
→ Kiểm tra MongoDB có keywords với `org_id` đó chưa:
```javascript
db.keyword.find({org_id: 2})
```

## Deploy lên server khác

1. Copy toàn bộ project
2. Chạy `install.bat` (hoặc install command)
3. Sửa `.env` với config đúng
4. Chạy `go run cmd/main.go`

**Hoặc build binary trên máy dev rồi copy:**
```bash
# Máy dev
go build -o bot.exe cmd/main.go

# Copy bot.exe + .env sang server
# Chạy trên server
./bot.exe
```

**Lưu ý:** Playwright driver phải cài trên từng máy riêng!
