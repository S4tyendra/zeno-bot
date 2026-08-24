package models

type VertexLinks struct {
	ID    string
	Links []GroundingLink
	Sent  bool
}

type GroundingLink struct {
	Title string `json:"title"`
	URI   string `json:"uri"`
}
