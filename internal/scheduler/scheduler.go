package scheduler

import (
	"abp-bot-tiktok/internal/crawler"
	"abp-bot-tiktok/pkg/config"
	"time"

	"go.uber.org/zap"
)

type Scheduler struct {
	cfg     *config.Config
	log     *zap.Logger
	crawler *crawler.Crawler
	stopCh  chan struct{}
}

func New(cfg *config.Config, log *zap.Logger, c *crawler.Crawler) *Scheduler {
	return &Scheduler{
		cfg:     cfg,
		log:     log,
		crawler: c,
		stopCh:  make(chan struct{}),
	}
}

func (s *Scheduler) Start() {
	s.log.Info("Scheduler started with interval mode", zap.String("interval", "90 minutes"))

	// Run immediately on startup
	s.log.Info("Running initial crawl on startup...")
	s.crawler.Run()

	// Start interval loop
	go s.runInterval()
}

func (s *Scheduler) runInterval() {
	ticker := time.NewTicker(90 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.log.Info("Interval triggered - starting new crawl cycle")
			s.crawler.Run()
			s.log.Info("Crawl cycle completed - waiting 90 minutes for next cycle")
		case <-s.stopCh:
			s.log.Info("Scheduler stopped")
			return
		}
	}
}

func (s *Scheduler) Stop() {
	close(s.stopCh)
	s.log.Info("Scheduler stopping...")
}
