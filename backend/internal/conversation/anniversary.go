package conversation

import "time"

type Anniversary struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Date   string `json:"date"`
	Kind   string `json:"kind"`
	Pinned bool   `json:"pinned"`
}
type AnniversaryBoard struct {
	SpaceCreatedAt time.Time     `json:"spaceCreatedAt"`
	Items          []Anniversary `json:"items"`
	HasMore        bool          `json:"hasMore"`
}
