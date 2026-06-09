package warning

import (
	"encoding/json"
	"fmt"

	"abp-bot-tiktok/pkg/config"
	"abp-bot-tiktok/pkg/gpm"

	"github.com/playwright-community/playwright-go"
	"go.uber.org/zap"
)

// Message is the payload published to manual.warnings.{source} by the ABP Telegram Bot.
// Format sent by users: link=<url> source=<SOURCE> orgId=<id>
type Message struct {
	Link   string `json:"link"`
	Source string `json:"source"`
	OrgID  int    `json:"orgId"`
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

	h.log.Sugar().Infof("[warning] source=%s orgId=%d profile=%s link=%s", msg.Source, msg.OrgID, profileID[:8], msg.Link)
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
	return nil
}
