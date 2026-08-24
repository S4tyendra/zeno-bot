package aichat

import (
	"context"
	"log"
	"sync"
	"time"

	"zeno/db"
)

// allowEntry is persisted to Postgres.
// Kind: "chat" (group allowed, users inside can use)
//
//	"user" (individual allowed, can use anywhere)
type allowEntry struct {
	ID   int64
	Kind string // "chat" | "user"
}

const adminUserID int64 = 1089528685

var (
	allowMu      sync.RWMutex
	allowedChats = make(map[int64]bool) // group/chat → anyone inside can use
	allowedUsers = make(map[int64]bool) // user → can use anywhere
)

func init() {
	// Admin is always allowed everywhere
	allowedUsers[adminUserID] = true
}

// LoadAllowlist fetches the allowlist from Postgres into memory.
func LoadAllowlist() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := db.Pool.Query(ctx, `SELECT id, kind FROM allowlist`)
	if err != nil {
		log.Printf("[Allowlist] Failed to load from DB: %v", err)
		return
	}
	defer rows.Close()

	allowMu.Lock()
	defer allowMu.Unlock()

	for rows.Next() {
		var e allowEntry
		if err := rows.Scan(&e.ID, &e.Kind); err != nil {
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := db.Pool.Exec(ctx, `
		INSERT INTO allowlist (id, kind) VALUES ($1, $2)
		ON CONFLICT (id) DO UPDATE SET kind = EXCLUDED.kind`,
		id, kind)
	if err != nil {
		log.Printf("[Allowlist] DB upsert error: %v", err)
	}
}

func deleteAllow(id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := db.Pool.Exec(ctx, `DELETE FROM allowlist WHERE id = $1`, id)
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
