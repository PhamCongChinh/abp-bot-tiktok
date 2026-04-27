package main

import (
	"abp-bot-tiktok/internal/crawler"
	"abp-bot-tiktok/internal/repository"
	"abp-bot-tiktok/internal/scheduler"
	"abp-bot-tiktok/pkg/config"
	"abp-bot-tiktok/pkg/database"
	"abp-bot-tiktok/pkg/logger"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
)

func main() {
	cfg := config.Load()
	log := logger.New(cfg.LogLevel)
	defer log.Sync()

	log.Info("Starting abp-bot-tiktok...")
	log.Sugar().Infof("DEBUG=%v | BotName=%s", cfg.Debug, cfg.BotName)

	// Connect to MongoDB
	mongoDB, err := database.NewMongoDB(cfg.MongoURI, cfg.MongoDB, log)
	if err != nil {
		log.Fatal("Failed to connect MongoDB", zap.Error(err))
	}
	defer mongoDB.Close()

	// Init repositories
	// videoRepo := repository.NewVideoRepository(mongoDB.Database(), log)
	keywordRepo := repository.NewKeywordRepository(mongoDB.Database(), log)

	// Load keywords from MongoDB by org_ids from .env
	log.Info("Loading keywords from MongoDB", zap.Ints("org_ids", cfg.OrgIDs))

	keywords, err := keywordRepo.FindByOrgIDs(cfg.OrgIDs)
	if err != nil {
		log.Fatal("Failed to load keywords from MongoDB", zap.Error(err))
	}

	log.Info("Keywords loaded from MongoDB",
		zap.Int("count", len(keywords)),
		zap.Ints("org_ids", cfg.OrgIDs),
	)

	// Build keyword list and group by org_id
	orgKeywordCount := make(map[int]int)
	var keywordList []string
	for _, kw := range keywords {
		keywordList = append(keywordList, kw.Keyword)
		orgKeywordCount[kw.OrgID]++
	}

	log.Info("Keywords distribution by organization:")
	for orgID, count := range orgKeywordCount {
		log.Info("", zap.Int("org_id", orgID), zap.Int("keywords", count))
	}

	if len(keywordList) == 0 {
		log.Warn("No keywords found for org_ids, exiting", zap.Ints("org_ids", cfg.OrgIDs))
		return
	}

	// Set keywords to config (will be reused for all crawl cycles)
	cfg.Keywords = keywordList

	// Init crawler
	c := crawler.New(cfg, log, nil)
	
	log.Info("Crawler initialized - will crawl same keywords every 1-1.5 hours")
	
	runCrawler(cfg, log, c)
}

func runCrawler(cfg *config.Config, log *zap.Logger, c *crawler.Crawler) {

	if cfg.Debug {
		// Chạy thẳng, không cần cron
		log.Info("DEBUG mode: running crawler immediately")
		c.Run()
		log.Info("Done.")
		return
	}

	// Production: dùng scheduler
	s := scheduler.New(cfg, log, c)
	s.Start()
	defer s.Stop()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("Shutting down...")
}
