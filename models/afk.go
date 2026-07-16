package models

import "time"

type AFKState struct {
	UserID   int64     `bson:"_id"`
	Username string    `bson:"username,omitempty"`
	AFKTime  time.Time `bson:"afk_time"`
	Reason   string    `bson:"reason,omitempty"`
}
