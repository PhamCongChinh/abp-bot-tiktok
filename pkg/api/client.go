package api

import (
	"abp-bot-tiktok/internal/parser"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
	log        *zap.Logger
}

type postRequest struct {
	Index  string              `json:"index"`
	Data   []parser.TiktokPost `json:"data"`
	Upsert bool                `json:"upsert"`
}

type postResponse struct {
	Success bool   `json:"success"`
	Total   int    `json:"total"`
	Status  int    `json:"status"`
	Error   string `json:"error"`
}

func NewClient(baseURL string, log *zap.Logger) *Client {
	// Normalize baseURL - strip trailing slash, add http:// if missing
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "http://" + baseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		log: log,
	}
}

func (c *Client) PostUnclassified(posts []parser.TiktokPost) error {
	return c.post("/api/v1/posts/insert-unclassified-org-posts", "not_classify_org_posts", posts)
}

func (c *Client) PostClassified(posts []parser.TiktokPost) error {
	return c.post("/api/v1/posts/insert-posts", "classify_org_posts", posts)
}

func (c *Client) post(path, index string, posts []parser.TiktokPost) error {
	if len(posts) == 0 {
		return nil
	}

	payload := postRequest{
		Index:  index,
		Data:   posts,
		Upsert: true,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	url := fmt.Sprintf("%s%s", c.baseURL, path)
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

	if resp.StatusCode >= 400 {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	return nil
}
