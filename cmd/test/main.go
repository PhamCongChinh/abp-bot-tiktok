package main

import (
	"abp-bot-tiktok/internal/crawler"
	"abp-bot-tiktok/internal/repository"
	"abp-bot-tiktok/pkg/config"
	"abp-bot-tiktok/pkg/database"
	"abp-bot-tiktok/pkg/logger"
	"os"
	"strings"
)

func main() {
	cfg := config.Load()
	log := logger.New("debug")
	defer log.Sync()

	log.Info("=== LOCAL TEST MODE ===")

	keywords := getTestKeywords()
	log.Sugar().Infof("Test keywords: %v", keywords)

	cfg.Keywords = keywords
	cfg.KeywordOrgMap = make(map[string]int)
	for _, kw := range keywords {
		cfg.KeywordOrgMap[kw] = 0
	}

	c := crawler.New(cfg, log, nil)

	// Connect to Postgres (optional)
	if cfg.PostgresURI != "" {
		postgresDB, err := database.NewPostgresDB(cfg.PostgresURI, log)
		if err != nil {
			log.Sugar().Warnf("PostgreSQL connect failed, article fetch disabled: %v", err)
		} else {
			defer postgresDB.Close()
			articleRepo := repository.NewArticleRepository(postgresDB.DB, cfg.ArticleTable, log)
			c.SetArticleRepo(articleRepo)
			log.Info("PostgreSQL connected - will fetch articles between keywords")
		}
	}

	c.RunLocal(keywords)
}

func getTestKeywords() []string {
	if v := os.Getenv("TEST_KEYWORDS"); v != "" {
		var result []string
		for _, kw := range strings.Split(v, ",") {
			kw = strings.TrimSpace(kw)
			if kw != "" {
				result = append(result, kw)
			}
		}
		if len(result) > 0 {
			return result
		}
	}
	return []string{"golang", "tiktok viral"}
}
