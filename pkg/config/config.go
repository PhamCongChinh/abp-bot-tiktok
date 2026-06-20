package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	LogLevel  string
	Debug     bool
	BotName   string
	// GoLogin launcher sidecar
	GoLoginLauncherURL string
	// TikTok profile IDs (comma-separated)
	TikTokProfileIDs []string
	// Postgres
	PostgresDSN string
	// MinIO
	MinIOEndpoint  string
	MinIOAccessKey string
	MinIOSecretKey string
	MinIOBucket    string
	// Crawler settings
	SourceID            string
	ClaimChunk          int
	ClaimPollIntervalMS int
	TikTokContentPageCap int
}

func Load() *Config {
	_ = godotenv.Load()

	profileIDsStr := getEnv("TIKTOK_PROFILE_IDS", "")
	var profileIDs []string
	if profileIDsStr != "" {
		for _, p := range strings.Split(profileIDsStr, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				profileIDs = append(profileIDs, p)
			}
		}
	}

	return &Config{
		LogLevel:             getEnv("LOG_LEVEL", "info"),
		Debug:                getEnv("DEBUG", "false") == "true",
		BotName:              getEnv("BOT_NAME", "abp-bot-tiktok"),
		GoLoginLauncherURL:   getEnv("GOLOGIN_LAUNCHER_URL", ""),
		TikTokProfileIDs:     profileIDs,
		PostgresDSN:          getEnv("POSTGRES_DSN", ""),
		MinIOEndpoint:        getEnv("MINIO_ENDPOINT", ""),
		MinIOAccessKey:       getEnv("MINIO_ACCESS_KEY", ""),
		MinIOSecretKey:       getEnv("MINIO_SECRET_KEY", ""),
		MinIOBucket:          getEnv("MINIO_BUCKET", ""),
		SourceID:             getEnv("SOURCE_ID", "scraper_tiktok"),
		ClaimChunk:           getEnvInt("CLAIM_CHUNK", 5),
		ClaimPollIntervalMS:  getEnvInt("CLAIM_POLL_INTERVAL_MS", 10000),
		TikTokContentPageCap: getEnvInt("TIKTOK_CONTENT_PAGE_CAP", 10),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return n
}
