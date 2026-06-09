package warning

import (
	"encoding/json"
	"fmt"

	"abp-bot-tiktok/internal/utils"
	"abp-bot-tiktok/pkg/config"
	"abp-bot-tiktok/pkg/gpm"

	"github.com/playwright-community/playwright-go"
	"go.uber.org/zap"
)

// Message matches the PostEntity payload published to manual.warnings.{source}.
type Message struct {
	ID              string  `json:"id"`
	DocType         int     `json:"doc_type"`
	SourceType      int     `json:"source_type"`
	CrawlSource     int     `json:"crawl_source"`
	CrawlSourceCode string  `json:"crawl_source_code"`
	PubTime         int64   `json:"pub_time"`
	CrawlTime       int64   `json:"crawl_time"`
	OrgID           int     `json:"org_id"`
	SubjectID       string  `json:"subject_id"`
	Title           string  `json:"title"`
	Description     string  `json:"description"`
	Content         string  `json:"content"`
	URL             string  `json:"url"`
	MediaURLs       string  `json:"media_urls"`
	Comments        int64   `json:"comments"`
	Shares          int64   `json:"shares"`
	Reactions       int64   `json:"reactions"`
	Favors          int64   `json:"favors"`
	Views           int64   `json:"views"`
	WebTags         string  `json:"web_tags"`
	WebKeywords     string  `json:"web_keywords"`
	AuthID          string  `json:"auth_id"`
	AuthName        string  `json:"auth_name"`
	AuthType        int     `json:"auth_type"`
	AuthURL         string  `json:"auth_url"`
	SourceID        string  `json:"source_id"`
	SourceName      string  `json:"source_name"`
	SourceURL       string  `json:"source_url"`
	ReplyTo         *string `json:"reply_to"`
	Level           int     `json:"level"`
	Sentiment       int     `json:"sentiment"`
	IsPriority      bool    `json:"isPriority"`
	CrawlBot        string  `json:"crawl_bot"`
	Link            string  `json:"link"`
	Source          string  `json:"source"`
	Status          string  `json:"status"`
	ErrorMessage    *string `json:"error_message"`
}

type Handler struct {
	cfg *config.Config
	log *zap.Logger
	pw  *playwright.Playwright
}

func NewHandler(cfg *config.Config, log *zap.Logger) (*Handler, error) {
	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("playwright start: %w", err)
	}
	return &Handler{cfg: cfg, log: log, pw: pw}, nil
}

func (h *Handler) Close() {
	h.pw.Stop()
}

func (h *Handler) Handle(data []byte) error {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		// treat raw value as plain URL
		msg.Link = string(data)
	}

	if msg.Link == "" {
		h.log.Warn("[warning] empty link in message, skipping")
		return nil
	}

	profileID := h.cfg.WarningProfileID
	if profileID == "" && len(h.cfg.ProfileIDs) > 0 {
		profileID = h.cfg.ProfileIDs[0]
	}
	if profileID == "" {
		return fmt.Errorf("[warning] no GPM profile ID available")
	}

	h.log.Sugar().Infof("[warning] id=%s source=%s org_id=%d auth=%s sentiment=%d priority=%v link=%s",
		msg.ID, msg.Source, msg.OrgID, msg.AuthName, msg.Sentiment, msg.IsPriority, msg.Link)
	return h.gotoURL(profileID, msg.Link)
}

// gotoURL opens a GPM profile and navigates to targetURL.
// The browser is intentionally left open for manual review.
func (h *Handler) gotoURL(profileID, targetURL string) error {
	gpmClient := gpm.NewClient(h.cfg.GPMAPI, h.log)

	wsURL, err := gpmClient.StartProfile(profileID)
	if err != nil {
		return fmt.Errorf("GPM start: %w", err)
	}

	browser, err := h.pw.Chromium.ConnectOverCDP(wsURL)
	if err != nil {
		return fmt.Errorf("CDP connect: %w", err)
	}

	contexts := browser.Contexts()
	if len(contexts) == 0 {
		return fmt.Errorf("no browser context from GPM")
	}

	page, err := contexts[0].NewPage()
	if err != nil {
		return fmt.Errorf("new page: %w", err)
	}

	if _, err := page.Goto(targetURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	}); err != nil {
		return fmt.Errorf("goto %s: %w", targetURL, err)
	}

	h.log.Sugar().Infof("[warning] navigated to %s", targetURL)

	// Scroll 10-15 times, 1-3s between each scroll
	scrollTimes := utils.RandInt(10, 15)
	h.log.Sugar().Infof("[warning] scrolling %d times...", scrollTimes)
	for i := 0; i < scrollTimes; i++ {
		page.Mouse().Move(
			float64(utils.RandInt(300, 700)),
			float64(utils.RandInt(200, 500)),
		)
		page.Mouse().Wheel(0, float64(utils.RandInt(600, 900)))
		h.log.Sugar().Infof("[warning] scroll %d/%d", i+1, scrollTimes)
		utils.Sleep(1000, 3000)
	}

	h.log.Sugar().Infof("[warning] done scrolling")
	return nil
}
