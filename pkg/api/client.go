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

type WarningPost struct {
	DocType         int        `json:"doc_type"`
	CrawlSource     int        `json:"crawl_source"`
	CrawlSourceCode string     `json:"crawl_source_code"`
	OrgID           int        `json:"org_id"`
	PubTime         int64      `json:"pub_time"`
	CrawlTime       int64      `json:"crawl_time"`
	SubjectID       string     `json:"subject_id"`
	Title           *string    `json:"title"`
	Description     string     `json:"description"`
	Content         string     `json:"content"`
	URL             string     `json:"url"`
	MediaURLs       string     `json:"media_urls"`
	Comments        int64      `json:"comments"`
	Shares          int64      `json:"shares"`
	Reactions       int64      `json:"reactions"`
	Favors          int64      `json:"favors"`
	Views           int64      `json:"views"`
	WebTags         string     `json:"web_tags"`
	WebKeywords     string     `json:"web_keywords"`
	AuthID          string     `json:"auth_id"`
	AuthName        string     `json:"auth_name"`
	AuthType        int        `json:"auth_type"`
	AuthURL         string     `json:"auth_url"`
	SourceID        string     `json:"source_id"`
	SourceType      int        `json:"source_type"`
	SourceName      string     `json:"source_name"`
	SourceURL       string     `json:"source_url"`
	ReplyTo         *string    `json:"reply_to"`
	Level           *int       `json:"level"`
	Sentiment       int        `json:"sentiment"`
	IsPriority      bool       `json:"isPriority"`
	CrawlBot        string     `json:"crawl_bot"`
	Link            string     `json:"link"`
	Source          string     `json:"source"`
	Status          string     `json:"status"`
	ErrorMessage    *string    `json:"error_message,omitempty"`
	CrawledAt       time.Time  `json:"crawledAt"`
}

type warningPostRequest struct {
	Index  string        `json:"index"`
	Data   []WarningPost `json:"data"`
	Upsert bool          `json:"upsert"`
}

func FromTiktokPost(p parser.TiktokPost, link, source string) WarningPost {
	return WarningPost{
		DocType: p.DocType, CrawlSource: p.CrawlSource, CrawlSourceCode: p.CrawlSourceCode,
		OrgID: p.OrgID, PubTime: p.PubTime, CrawlTime: p.CrawlTime,
		SubjectID: p.SubjectID, Title: p.Title, Description: p.Description, Content: p.Content,
		URL: p.URL, MediaURLs: p.MediaURLs,
		Comments: p.Comments, Shares: p.Shares, Reactions: p.Reactions, Favors: p.Favors, Views: p.Views,
		WebTags: p.WebTags, WebKeywords: p.WebKeywords,
		AuthID: p.AuthID, AuthName: p.AuthName, AuthType: p.AuthType, AuthURL: p.AuthURL,
		SourceID: p.SourceID, SourceType: p.SourceType, SourceName: p.SourceName, SourceURL: p.SourceURL,
		ReplyTo: p.ReplyTo, Level: p.Level, Sentiment: p.Sentiment, IsPriority: p.IsPriority,
		CrawlBot: p.CrawlBot,
		Link: link, Source: source, Status: "SUCCESS", CrawledAt: time.Now(),
	}
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

// FailedWarningPost tạo WarningPost với status=FAILED và error_message
func FailedWarningPost(link, source string, orgID int, reason string) WarningPost {
	return WarningPost{
		OrgID: orgID, Link: link, Source: source,
		Status: "FAILED", ErrorMessage: &reason,
		CrawledAt: time.Now(),
	}
}

func (c *Client) PostWarning(posts []parser.TiktokPost, link, source string) error {
	if len(posts) == 0 {
		return nil
	}
	var data []WarningPost
	for _, p := range posts {
		data = append(data, FromTiktokPost(p, link, source))
	}
	payload := warningPostRequest{Index: "classify_org_posts", Data: data, Upsert: true}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	url := fmt.Sprintf("%s/api/v1/posts/insert-posts", c.baseURL)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	return nil
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
