package models

import "time"

type Article struct {
	ID              int64
	OrgID           int
	DocType         string
	SourceType      string
	CrawlSource     string
	CrawlSourceCode string
	PubTime         time.Time
	CrawlTime       time.Time
	SubjectID       *int64
	Title           string
	Description     string
	Content         string
	URL             string
	MediaURLs       []string
	Comments        int64
	Shares          int64
	Reactions       int64
	Favors          int64
	Views           int64
	WebTags         []string
	WebKeywords     []string
	AuthID          string
	AuthName        string
	AuthType        string
	SourceID        string
	SourceName      string
	ReplyTo         *int64
	Level           int
	SourceURL       string
	AuthURL         string
	SourceOwnership string
}
