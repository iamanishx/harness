package types

type Session struct {
	ID          string `json:"id"`
	CWD         string `json:"cwd"`
	Title       string `json:"title"`
	TimeCreated int64  `json:"time_created"`
	TimeUpdated int64  `json:"time_updated"`
}
