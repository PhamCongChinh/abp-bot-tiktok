package main

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	warningHandler, err := warning.NewHandler(cfg, log)
	if err != nil {
		log.Fatal("Failed to init warning handler", zap.Error(err))
	}
	defer warningHandler.Close()

	if cfg.Debug {
		log.Info("DEBUG mode: injecting fake warning message")
		runFake(warningHandler, log)
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
	log.Info("Kafka consumer started", zap.String("topic", "manual.warnings.tiktok"), zap.Strings("brokers", cfg.KafkaBrokers))

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("Shutting down...")
}

func runFake(h *warning.Handler, log *zap.Logger) {
	replyTo := (*string)(nil)
	errMsg := (*string)(nil)

	msg := warning.Message{
		ID:              "7389201034567890123",
		DocType:         1,
		SourceType:      3,
		CrawlSource:     5,
		CrawlSourceCode: "TIKTOK",
		PubTime:         time.Now().Add(-24 * time.Hour).Unix(),
		CrawlTime:       time.Now().Unix(),
		OrgID:           786859,
		SubjectID:       "subj_001",
		Description:     "Fake warning post for testing",
		Content:         "Fake warning post for testing",
		URL:             "https://www.tiktok.com/@meelayraydog",
		AuthID:          "6823401234567890",
		AuthName:        "meelayraydog",
		AuthType:        1,
		AuthURL:         "https://www.tiktok.com/@meelayraydog",
		SourceID:        "6823401234567890",
		SourceName:      "meelayraydog",
		SourceURL:       "https://www.tiktok.com/@meelayraydog",
		ReplyTo:         replyTo,
		Level:           1,
		Sentiment:       -1,
		IsPriority:      true,
		CrawlBot:        "abp-bot-tiktok",
		Link:            "https://www.tiktok.com/@meelayraydog",
		Source:          "TIKTOK",
		Status:          "pending",
		ErrorMessage:    errMsg,
	}

	data, _ := json.Marshal(msg)
	if err := h.Handle(data); err != nil {
		log.Error("Fake handler error", zap.Error(err))
	} else {
		log.Info("Fake message handled successfully")
	}
}
