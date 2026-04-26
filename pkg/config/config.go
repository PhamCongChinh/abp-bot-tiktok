package config

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	LogLevel     string
	CronSchedule string
	OutputDir    string
	Debug        bool
	BotName      string
	// MongoDB
	MongoURI string
	MongoDB  string
	OrgID    int
	// API endpoint to push data
	APIURL string
	// GPM (GoLogin Profile Manager) - support multiple profiles
	GPMAPI     string
	ProfileIDs []string // Multiple profile IDs separated by comma
	UseGPM     bool
	// Keywords (loaded from MongoDB or .env)
	Keywords []string
	// Batch settings
	BatchMin int
	BatchMax int
	// Sleep between keywords (seconds)
	SleepMinKeyword int
	SleepMaxKeyword int
	// Rest between sessions (seconds)
	RestMinSession int
	RestMaxSession int
}

func Load() *Config {
	_ = godotenv.Load()

	gpmAPI := getEnv("GPM_API", "")
	profileIDsStr := getEnv("PROFILE_IDS", "")
	var profileIDs []string
	if profileIDsStr != "" {
		profileIDs = splitEnv("PROFILE_IDS", []string{})
	}

	return &Config{
		LogLevel:        getEnv("LOG_LEVEL", "info"),
		CronSchedule:    getEnv("CRON_SCHEDULE", "0 */6 * * *"),
		OutputDir:       getEnv("OUTPUT_DIR", "./data"),
		Debug:           getEnv("DEBUG", "true") == "true",
		BotName:         getEnv("BOT_NAME", "bot-test"),
		MongoURI:        getEnv("MONGO_URI", "mongodb://localhost:27017"),
		MongoDB:         getEnv("MONGO_DB", "tiktok_crawler"),
		OrgID:           getEnvInt("ORG_ID", 2),
		APIURL:          getEnv("API_URL", ""),
		GPMAPI:          gpmAPI,
		ProfileIDs:      profileIDs,
		UseGPM:          gpmAPI != "" && len(profileIDs) > 0,
		BatchMin:        getEnvInt("BATCH_MIN", 5),
		BatchMax:        getEnvInt("BATCH_MAX", 10),
		SleepMinKeyword: getEnvInt("SLEEP_MIN_KEYWORD", 60),
		SleepMaxKeyword: getEnvInt("SLEEP_MAX_KEYWORD", 120),
		RestMinSession:  getEnvInt("REST_MIN_SESSION", 600),
		RestMaxSession:  getEnvInt("REST_MAX_SESSION", 900),
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
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return fallback
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func splitEnv(key string, fallback []string) []string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var result []string
	start := 0
	for i := 0; i <= len(v); i++ {
		if i == len(v) || v[i] == ',' {
			part := v[start:i]
			if part != "" {
				result = append(result, part)
			}
			start = i + 1
		}
	}
	return result
}
