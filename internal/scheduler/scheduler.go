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
	// Interval between crawl cycles (30-45 minutes)
	intervalMin := 30  // 30 minutes
	intervalMax := 45  // 45 minutes
	
	s.log.Info("Scheduler started with interval mode", 
		zap.Int("interval_min_minutes", intervalMin),
		zap.Int("interval_max_minutes", intervalMax),
	)

	// Run immediately on startup
	s.log.Info("Running initial crawl on startup...")
	s.crawler.Run()

	// Start interval loop
	go s.runInterval(intervalMin, intervalMax)
}

func (s *Scheduler) runInterval(minMinutes, maxMinutes int) {
	for {
		// Random interval between min and max
		intervalMinutes := minMinutes + int(time.Now().UnixNano()%int64(maxMinutes-minMinutes+1))
		interval := time.Duration(intervalMinutes) * time.Minute
		
		s.log.Info("Waiting for next crawl cycle", 
			zap.Int("minutes", intervalMinutes),
			zap.String("next_run_at", time.Now().Add(interval).Format("2006-01-02 15:04:05")),
		)
		
		timer := time.NewTimer(interval)
		
		select {
		case <-timer.C:
			s.log.Info("========================================")
			s.log.Info("Interval triggered - starting new crawl cycle")
			s.log.Info("========================================")
			s.crawler.Run()
		case <-s.stopCh:
			timer.Stop()
			s.log.Info("Scheduler stopped")
			return
		}
	}
}

func (s *Scheduler) Stop() {
	close(s.stopCh)
	s.log.Info("Scheduler stopping...")
}
