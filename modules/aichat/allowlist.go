package aichat

import (
	"context"
	"log"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	"zeno/db"
)

// allowEntry is persisted to MongoDB.
// Kind: "chat" (group allowed, users inside can use)
//       "user" (individual allowed, can use anywhere)
type allowEntry struct {
	ID   int64  `bson:"_id"`
	Kind string `bson:"kind"` // "chat" | "user"
}

const adminUserID int64 = 1089528685

var (
	allowMu       sync.RWMutex
	allowedChats  = make(map[int64]bool) // group/chat → anyone inside can use
	allowedUsers  = make(map[int64]bool) // user → can use anywhere
)

func init() {
	// Admin is always allowed everywhere
	allowedUsers[adminUserID] = true
}

// LoadAllowlist fetches the allowlist from MongoDB into memory.
func LoadAllowlist() {
	col := db.Collection("allowlist")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cur, err := col.Find(ctx, bson.M{})
	if err != nil {
		log.Printf("[Allowlist] Failed to load from DB: %v", err)
		return
	}
	defer cur.Close(ctx)

	allowMu.Lock()
	defer allowMu.Unlock()

	for cur.Next(ctx) {
		var e allowEntry
		if err := cur.Decode(&e); err != nil {
			continue
		}
		switch e.Kind {
		case "chat":
			allowedChats[e.ID] = true
		case "user":
			allowedUsers[e.ID] = true
		}
	}
	log.Printf("[Allowlist] Loaded %d chats, %d users from DB", len(allowedChats), len(allowedUsers))
}

func upsertAllow(id int64, kind string) {
	col := db.Collection("allowlist")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := col.UpdateOne(ctx,
		bson.M{"_id": id},
		bson.M{"$set": bson.M{"_id": id, "kind": kind}},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		log.Printf("[Allowlist] DB upsert error: %v", err)
	}
}

func deleteAllow(id int64) {
	col := db.Collection("allowlist")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := col.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		log.Printf("[Allowlist] DB delete error: %v", err)
	}
}

// FilterAllowed returns true if the sender/chat is allowed to use AI.
func FilterAllowed(m interface {
	ChatID() int64
	SenderID() int64
	IsPrivate() bool
}) bool {
	allowMu.RLock()
	defer allowMu.RUnlock()

	// Admin always allowed
	if m.SenderID() == adminUserID {
		return true
	}
	// Individual user allowed (can use anywhere)
	if allowedUsers[m.SenderID()] {
		return true
	}
	// Chat allowed (only in that specific chat)
	if !m.IsPrivate() && allowedChats[m.ChatID()] {
		return true
	}
	return false
}

// AddAllowChat adds a group/chat to the allowlist.
func AddAllowChat(chatID int64) {
	allowMu.Lock()
	allowedChats[chatID] = true
	allowMu.Unlock()
	upsertAllow(chatID, "chat")
}

// AddAllowUser adds a user to the allowlist (can use anywhere).
func AddAllowUser(userID int64) {
	allowMu.Lock()
	allowedUsers[userID] = true
	allowMu.Unlock()
	upsertAllow(userID, "user")
}

// RemoveAllow removes an ID (either kind) from the allowlist.
func RemoveAllow(id int64) {
	allowMu.Lock()
	delete(allowedChats, id)
	delete(allowedUsers, id)
	allowMu.Unlock()
	deleteAllow(id)
}
