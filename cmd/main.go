package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"abp-bot-tiktok/internal/crawler"
	"abp-bot-tiktok/internal/repository"
	"abp-bot-tiktok/internal/scheduler"
	"abp-bot-tiktok/internal/warning"
	"abp-bot-tiktok/pkg/config"
	"abp-bot-tiktok/pkg/database"
	kafkaconsumer "abp-bot-tiktok/pkg/kafka"
	"abp-bot-tiktok/pkg/logger"

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

	// Build keyword list, org map, and group by org_id
	orgKeywordCount := make(map[int]int)
	var keywordList []string
	keywordOrgMap := make(map[string]int)
	for _, kw := range keywords {
		keywordList = append(keywordList, kw.Keyword)
		keywordOrgMap[kw.Keyword] = kw.OrgID
		orgKeywordCount[kw.OrgID]++
	}

	log.Info("Keywords distribution by organization:")
	for orgID, count := range orgKeywordCount {
		log.Info("", zap.Int("org_id", orgID), zap.Int("keywords", count))
	}

	cfg.Keywords = keywordList
	cfg.KeywordOrgMap = keywordOrgMap

	// Graceful shutdown context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Always start Kafka consumer for manual warnings
	if len(cfg.KafkaBrokers) > 0 {
		warningHandler, err := warning.NewHandler(cfg, log)
		if err != nil {
			log.Fatal("Failed to init warning handler", zap.Error(err))
		}
		defer warningHandler.Close()

		consumer := kafkaconsumer.NewConsumer(cfg.KafkaBrokers, "manual.warnings.tiktok", cfg.KafkaGroupID, log)
		defer consumer.Close()

		go consumer.Consume(ctx, warningHandler.Handle)
		log.Info("Kafka consumer started", zap.String("topic", "manual.warnings.tiktok"), zap.Strings("brokers", cfg.KafkaBrokers))
	} else {
		log.Warn("KAFKA_BROKERS not set — warning consumer disabled")
	}

	// Run crawler (keywords must be loaded)
	if len(keywordList) == 0 {
		log.Warn("No keywords found for org_ids", zap.Ints("org_ids", cfg.OrgIDs))
	} else {
		c := crawler.New(cfg, log, nil)
		log.Info("Crawler initialized - will crawl same keywords every 1-1.5 hours")
		go runCrawler(cfg, log, c)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("Shutting down...")
}

func runCrawler(cfg *config.Config, log *zap.Logger, c *crawler.Crawler) {
	if cfg.Debug {
		log.Info("DEBUG mode: running crawler immediately")
		c.Run()
		log.Info("Crawler done.")
		return
	}

	s := scheduler.New(cfg, log, c)
	s.Start()
	defer s.Stop()

	// block until process exits (main goroutine handles signal)
	select {}
}
