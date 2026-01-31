package models

import "go.mongodb.org/mongo-driver/bson/primitive"

type VertexLinks struct {
	ID    primitive.ObjectID `bson:"_id,omitempty"`
	Links []GroundingLink    `bson:"links"`
	Sent  bool               `bson:"sent"`
}

type GroundingLink struct {
	Title string `bson:"title"`
	URI   string `bson:"uri"`
}
