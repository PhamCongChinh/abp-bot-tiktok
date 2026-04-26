package parser

import (
	"abp-bot-tiktok/internal/models"
	"fmt"
	"time"
)

const (
	baseURL         = "https://www.tiktok.com"
	crawlSource     = 2
	crawlSourceCode = "tt"
	authType        = 1
	sourceType      = 5
	crawlBot        = "tiktok-1"
	docType         = 1
)

type TiktokPost struct {
	DocType         int     `json:"doc_type"`
	CrawlSource     int     `json:"crawl_source"`
	CrawlSourceCode string  `json:"crawl_source_code"`
	PubTime         int64   `json:"pub_time"`
	CrawlTime       int64   `json:"crawl_time"`
	SubjectID       string  `json:"subject_id"`
	Title           *string `json:"title"`
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
	SourceType      int     `json:"source_type"`
	SourceName      string  `json:"source_name"`
	SourceURL       string  `json:"source_url"`
	ReplyTo         *string `json:"reply_to"`
	Level           *int    `json:"level"`
	Sentiment       int     `json:"sentiment"`
	IsPriority      bool    `json:"isPriority"`
	CrawlBot        string  `json:"crawl_bot"`
}

func FromVideoItem(v models.VideoItem) TiktokPost {
	videoURL := buildVideoURL(v.UniqueID, v.VideoID)
	authorURL := buildAuthorURL(v.UniqueID)

	return TiktokPost{
		DocType:         docType,
		CrawlSource:     crawlSource,
		CrawlSourceCode: crawlSourceCode,
		PubTime:         v.PubTime,
		CrawlTime:       time.Now().Unix(),
		SubjectID:       v.VideoID,
		Title:           nil,
		Description:     v.Description,
		Content:         v.Description,
		URL:             videoURL,
		MediaURLs:       "[]",
		Comments:        v.Comments,
		Shares:          v.Shares,
		Reactions:       v.Reactions,
		Favors:          v.Favors,
		Views:           v.Views,
		WebTags:         "[]",
		WebKeywords:     "[]",
		AuthID:          v.AuthID,
		AuthName:        v.AuthName,
		AuthType:        authType,
		AuthURL:         authorURL,
		SourceID:        v.VideoID,
		SourceType:      sourceType,
		SourceName:      v.AuthName,
		SourceURL:       videoURL,
		ReplyTo:         nil,
		Level:           nil,
		Sentiment:       0,
		IsPriority:      false,
		CrawlBot:        crawlBot,
	}
}

func buildVideoURL(uniqueID, postID string) string {
	if postID == "" {
		return ""
	}
	return fmt.Sprintf("%s/@%s/video/%s", baseURL, uniqueID, postID)
}

func buildAuthorURL(uniqueID string) string {
	return fmt.Sprintf("%s/@%s", baseURL, uniqueID)
}
