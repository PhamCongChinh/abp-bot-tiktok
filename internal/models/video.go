package models

type VideoItem struct {
	Keyword     string `json:"keyword"`
	VideoID     string `json:"video_id"`
	Description string `json:"description"`
	PubTime     int64  `json:"pub_time"`
	UniqueID    string `json:"unique_id"`
	AuthID      string `json:"auth_id"`
	AuthName    string `json:"auth_name"`
	Comments    int64  `json:"comments"`
	Shares      int64  `json:"shares"`
	Reactions   int64  `json:"reactions"`
	Favors      int64  `json:"favors"`
	Views       int64  `json:"views"`
}
