package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"abp-bot-tiktok/internal/repository"
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

	// Load profile IDs từ MongoDB thay vì .env
	mongoDB, err := database.NewMongoDB(cfg.MongoURI, cfg.MongoDB, log)
	if err != nil {
		log.Fatal("Failed to connect MongoDB", zap.Error(err))
	}
	defer mongoDB.Close()

	configRepo := repository.NewTiktokConfigManualRepository(mongoDB.Database(), log)
	profileIDs, err := configRepo.GetProfileIDs()
	if err != nil {
		log.Fatal("Failed to load profile IDs from tiktok_configs_manual", zap.Error(err))
	}
	if len(profileIDs) == 0 {
		log.Fatal("No profile IDs found in tiktok_configs_manual")
	}
	cfg.ProfileIDs = profileIDs
	cfg.UseGPM = cfg.GPMAPI != "" && len(profileIDs) > 0
	log.Sugar().Infof("Loaded %d profile IDs from MongoDB: %v", len(profileIDs), profileIDs)

	warningHandler, err := warning.NewHandler(cfg, log)
	if err != nil {
		log.Fatal("Failed to init warning handler", zap.Error(err))
	}
	defer warningHandler.Close()

	if cfg.Debug {
		log.Info("DEBUG mode: running with hardcoded message")
		testMsg := `{"link":"https://www.tiktok.com/@camngotstudio/video/7609342119219645716","source":"Tiktok","orgId":"620225","isAlert":false}`
		if err := warningHandler.Handle([]byte(testMsg)); err != nil {
			log.Error("Handle error", zap.Error(err))
		}
		log.Info("Done.")
		return
	}

	if len(cfg.KafkaBrokers) == 0 {
		log.Fatal("KAFKA_BROKERS is required")
	}

	consumer := kafkaconsumer.NewConsumer(cfg.KafkaBrokers, "manual.warnings.tiktok", cfg.KafkaGroupID, log)
	defer consumer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go consumer.Consume(ctx, warningHandler.Handle)
	log.Info("Kafka consumer started",
		zap.String("topic", "manual.warnings.tiktok"),
		zap.Strings("brokers", cfg.KafkaBrokers),
	)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("Shutting down...")
}
