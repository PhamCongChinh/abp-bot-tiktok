FROM golang:1.26 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -ldflags="-s -w" -o /bot ./cmd/main.go

# playwright-go embeds the playwright server binary, which is glibc-linked.
# Alpine (musl) cannot run glibc binaries — use Debian slim as runtime.
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates tzdata && rm -rf /var/lib/apt/lists/*
WORKDIR /root/
COPY --from=builder /bot .
CMD ["./bot"]
