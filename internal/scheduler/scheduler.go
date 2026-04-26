package scheduler

import (
	"abp-bot-tiktok/internal/crawler"
	"abp-bot-tiktok/pkg/config"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
)

type Scheduler struct {
	cron    *cron.Cron
	cfg     *config.Config
	log     *zap.Logger
	crawler *crawler.Crawler
}

func New(cfg *config.Config, log *zap.Logger, c *crawler.Crawler) *Scheduler {
	return &Scheduler{
		cron:    cron.New(),
		cfg:     cfg,
		log:     log,
		crawler: c,
	}
}

func (s *Scheduler) Start() {
	_, err := s.cron.AddFunc(s.cfg.CronSchedule, func() {
		s.log.Info("Scheduler triggered crawl", zap.String("schedule", s.cfg.CronSchedule))
		s.crawler.Run()
	})
	if err != nil {
		s.log.Fatal("Invalid cron schedule", zap.String("schedule", s.cfg.CronSchedule), zap.Error(err))
	}

	s.cron.Start()
	s.log.Info("Scheduler started", zap.String("schedule", s.cfg.CronSchedule))

	// Run immediately on startup
	s.log.Info("Running initial crawl on startup...")
	go s.crawler.Run()
}

func (s *Scheduler) Stop() {
	s.cron.Stop()
	s.log.Info("Scheduler stopped")
}
