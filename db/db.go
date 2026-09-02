package db

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"zeno/config"
)

var (
	Pool      *pgxpool.Pool
	ErrNoRows = pgx.ErrNoRows
)

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS allowlist (
		id BIGINT PRIMARY KEY,
		kind TEXT NOT NULL CHECK (kind IN ('chat', 'user'))
	)`,
	`CREATE TABLE IF NOT EXISTS system_settings (
		key TEXT PRIMARY KEY,
		value JSONB NOT NULL DEFAULT '{}'::jsonb,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE TABLE IF NOT EXISTS vertex_links (
		id UUID PRIMARY KEY,
		links JSONB NOT NULL,
		sent BOOLEAN NOT NULL DEFAULT FALSE
	)`,
	`CREATE TABLE IF NOT EXISTS memories (
		user_id BIGINT NOT NULL,
		index INT NOT NULL,
		text TEXT NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (user_id, index)
	)`,
	`CREATE TABLE IF NOT EXISTS uploaded_files (
		chat_id BIGINT NOT NULL,
		msg_id INTEGER NOT NULL,
		google_file_uri TEXT NOT NULL DEFAULT '',
		mime_type TEXT NOT NULL DEFAULT '',
		file_name TEXT NOT NULL DEFAULT '',
		uploaded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (chat_id, msg_id)
	)`,
	`CREATE TABLE IF NOT EXISTS tool_history (
		chat_id BIGINT NOT NULL,
		msg_id INTEGER NOT NULL,
		tool_calls JSONB NOT NULL DEFAULT '[]'::jsonb,
		uploaded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (chat_id, msg_id)
	)`,
	`CREATE TABLE IF NOT EXISTS tasks (
		task_id TEXT PRIMARY KEY,
		chat_id BIGINT NOT NULL,
		msg_id INTEGER NOT NULL,
		command TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'running',
		log_path TEXT NOT NULL,
		started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		finished_at TIMESTAMPTZ
	)`,
	`CREATE TABLE IF NOT EXISTS artifacts (
		artifact_id TEXT PRIMARY KEY,
		file_path TEXT NOT NULL,
		mime_type TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE TABLE IF NOT EXISTS startup_chats (
		chat_id BIGINT PRIMARY KEY,
		added_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE TABLE IF NOT EXISTS afk (
		user_id BIGINT PRIMARY KEY,
		username TEXT NOT NULL DEFAULT '',
		afk_time TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		reason TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE IF NOT EXISTS download_tasks (
		id SERIAL PRIMARY KEY,
		task_type TEXT NOT NULL,
		chat_id BIGINT NOT NULL,
		reply_msg_id INTEGER NOT NULL DEFAULT 0,
		status_msg_id INTEGER NOT NULL DEFAULT 0,
		user_id BIGINT NOT NULL,
		url_or_path TEXT NOT NULL,
		custom_name TEXT NOT NULL DEFAULT '',
		file_path TEXT NOT NULL DEFAULT '',
		file_size BIGINT NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'queued',
		error_msg TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE INDEX IF NOT EXISTS idx_download_tasks_status_id ON download_tasks (status, id ASC)`,
	`CREATE INDEX IF NOT EXISTS idx_download_tasks_chat_reply ON download_tasks (chat_id, reply_msg_id)`,
	`CREATE INDEX IF NOT EXISTS idx_download_tasks_chat_status ON download_tasks (chat_id, status_msg_id)`,
}

func Connect() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(config.PostgresDSN())
	if err != nil {
		log.Fatal("Failed to parse Postgres config:", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	cfg.ConnConfig.ConnectTimeout = 10 * time.Second

	Pool, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatal("Failed to connect to Postgres:", err)
	}

	pingCtx, pingCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pingCancel()
	if err = Pool.Ping(pingCtx); err != nil {
		log.Fatal("Failed to ping Postgres:", err)
	}

	if err = migrate(ctx); err != nil {
		log.Fatal("Failed to migrate Postgres schema:", err)
	}

	log.Println("Connected to Postgres")
}

func Disconnect() {
	if Pool != nil {
		Pool.Close()
	}
}

func migrate(ctx context.Context) error {
	for _, stmt := range schemaStatements {
		if _, err := Pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	// Files live in GCS (gs://aidatax); drop leftover BYTEA blobs.
	if _, err := Pool.Exec(ctx, `ALTER TABLE uploaded_files DROP COLUMN IF EXISTS data`); err != nil {
		return err
	}
	return nil
}

func GetSetting(ctx context.Context, key string, dest any) error {
	var raw []byte
	if err := Pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, key).Scan(&raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, dest)
}

func SetSetting(ctx context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = Pool.Exec(ctx, `
		INSERT INTO system_settings (key, value, updated_at)
		VALUES ($1, $2::jsonb, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		key, string(raw))
	return err
}

var modelCache sync.Map

type cacheEntry struct {
	val string
	exp time.Time
}

func GetRuntimeModel(key, fallback string) string {
	if entry, ok := modelCache.Load(key); ok {
		ce := entry.(cacheEntry)
		if time.Now().Before(ce.exp) {
			return ce.val
		}
	}

	var dbVal string
	err := Pool.QueryRow(context.Background(), `SELECT value->>'model' FROM system_settings WHERE key = $1`, "model_"+strings.ToLower(key)).Scan(&dbVal)
	if err != nil || dbVal == "" {
		dbVal = fallback
	}

	modelCache.Store(key, cacheEntry{val: dbVal, exp: time.Now().Add(30 * time.Second)})
	return dbVal
}

func SetRuntimeModel(key, model string) error {
	err := SetSetting(context.Background(), "model_"+strings.ToLower(key), map[string]string{"model": model})
	if err == nil {
		modelCache.Store(key, cacheEntry{val: model, exp: time.Now().Add(30 * time.Second)})
	}
	return err
}
