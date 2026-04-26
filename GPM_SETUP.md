# GPM (GoLogin Profile Manager) Setup

## Giới thiệu

GPM cho phép sử dụng browser profile đã login TikTok sẵn, tránh phải login lại mỗi lần chạy.

## Cài đặt GPM

1. Download GPM: https://gologin.com/
2. Cài đặt và mở GPM
3. Tạo profile mới hoặc dùng profile có sẵn
4. Login TikTok trong profile đó

## Lấy thông tin GPM

### 1. GPM API URL
Mặc định: `http://localhost:50325/api/v1`

Kiểm tra trong GPM Settings → API

### 2. Profile ID
- Mở GPM
- Click vào profile muốn dùng
- Copy Profile ID từ URL hoặc profile info

## Cấu hình .env

```env
# Bật GPM mode
GPM_API=http://localhost:50325/api/v1
PROFILE_ID=your-profile-id-here

# Các config khác
ORG_ID=2
MONGO_URI=mongodb://localhost:27017
MONGO_DB=tiktok_crawler
```

## Chạy với GPM

```bash
go run cmd/main.go
```

Bot sẽ:
1. Gọi GPM API để start profile
2. Lấy remote debugging address
3. Connect Playwright qua CDP (Chrome DevTools Protocol)
4. Sử dụng browser đã login sẵn
5. Crawl TikTok
6. Tự động stop profile khi xong

## Chạy không dùng GPM (local Chrome)

Để tắt GPM, xóa hoặc comment GPM config trong .env:

```env
# GPM_API=
# PROFILE_ID=
```

Bot sẽ tự động dùng local Chrome thay vì GPM.

## Troubleshooting

### Lỗi: "Failed to connect GPM"
- Kiểm tra GPM đang chạy
- Kiểm tra GPM_API đúng (mặc định port 50325)
- Thử restart GPM

### Lỗi: "No browser context found from GPM"
- Profile chưa được start
- Thử start profile thủ công trong GPM trước
- Kiểm tra PROFILE_ID đúng

### Lỗi: "Failed to connect CDP"
- GPM profile đã đóng
- Port bị block bởi firewall
- Thử restart GPM và chạy lại

## So sánh GPM vs Local Chrome

| | GPM | Local Chrome |
|---|---|---|
| Login TikTok | ✅ Tự động (đã login sẵn) | ❌ Cần login thủ công |
| Anti-detect | ✅ Fingerprint riêng | ⚠️ Dễ bị detect |
| Multi-profile | ✅ Nhiều profile | ❌ Chỉ 1 profile |
| Setup | ⚠️ Cần cài GPM | ✅ Đơn giản |
| Performance | ⚠️ Hơi chậm | ✅ Nhanh |

## Khuyến nghị

- **Production**: Dùng GPM với profile đã login
- **Development/Test**: Dùng local Chrome
