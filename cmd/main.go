package main

import (
	"abp-bot-tiktok/pkg/config"
	"abp-bot-tiktok/pkg/logger"
)

func main() {
	cfg := config.Load()
	log := logger.New(cfg.LogLevel)
	defer func() { _ = log.Sync() }()

	log.Info("abp-bot-tiktok starting (wiring in T11)")
}
