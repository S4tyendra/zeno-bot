package models

type User struct {
	ID              int64  `bson:"_id"`
	CerebrasAPIKey  string `bson:"cerebras_api_key"`
}
