package api

import (
	"abp-bot-tiktok/internal/parser"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
	log        *zap.Logger
}

func NewClient(baseURL string, log *zap.Logger) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		log: log,
	}
}

func (c *Client) PostPosts(posts []parser.TiktokPost) error {
	if len(posts) == 0 {
		return nil
	}

	body, err := json.Marshal(posts)
	if err != nil {
		return fmt.Errorf("failed to marshal posts: %w", err)
	}

	url := fmt.Sprintf("%s/api/posts", c.baseURL)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	c.log.Info("Posts sent to API",
		zap.String("url", url),
		zap.Int("count", len(posts)),
		zap.Int("status", resp.StatusCode),
	)
	return nil
}
