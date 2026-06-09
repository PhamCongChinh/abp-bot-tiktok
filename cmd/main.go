package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"abp-bot-tiktok/internal/warning"
	"abp-bot-tiktok/pkg/config"
	kafkaconsumer "abp-bot-tiktok/pkg/kafka"
	"abp-bot-tiktok/pkg/logger"

	"go.uber.org/zap"
)

func main() {
	cfg := config.Load()
	log := logger.New(cfg.LogLevel)
	defer log.Sync()

	log.Info("Starting abp-bot-tiktok...")

	if len(cfg.KafkaBrokers) == 0 {
		log.Fatal("KAFKA_BROKERS is required")
	}

	warningHandler, err := warning.NewHandler(cfg, log)
	if err != nil {
		log.Fatal("Failed to init warning handler", zap.Error(err))
	}
	defer warningHandler.Close()

	consumer := kafkaconsumer.NewConsumer(cfg.KafkaBrokers, "manual.warnings.tiktok", cfg.KafkaGroupID, log)
	defer consumer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go consumer.Consume(ctx, warningHandler.Handle)
	log.Info("Kafka consumer started", zap.String("topic", "manual.warnings.tiktok"), zap.Strings("brokers", cfg.KafkaBrokers))

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("Shutting down...")
}
