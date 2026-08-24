package aichat

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
	"github.com/google/uuid"
	"google.golang.org/genai"

	"zeno/db"
	"zeno/models"
)

func storeGroundingLinks(chunks []*genai.GroundingChunk) (string, error) {
	links := make([]models.GroundingLink, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk.Web != nil {
			links = append(links, models.GroundingLink{
				Title: chunk.Web.Title,
				URI:   chunk.Web.URI,
			})
		}
	}

	if len(links) == 0 {
		return "", fmt.Errorf("no web links found")
	}

	linksJSON, err := json.Marshal(links)
	if err != nil {
		return "", err
	}

	id := uuid.New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = db.Pool.Exec(ctx, `INSERT INTO vertex_links (id, links, sent) VALUES ($1, $2::jsonb, false)`, id, string(linksJSON))
	if err != nil {
		return "", err
	}

	return id.String(), nil
}

func handleGetVertexLinks(cb *telegram.CallbackQuery) error {
	data := string(cb.Data)
	log.Printf("[AiChat] Callback received: %s from user %d", data, cb.Sender.ID)
	parts := strings.Split(data, "|")
	if len(parts) != 2 {
		cb.Answer("Invalid request", &telegram.CallbackOptions{Alert: true})
		return nil
	}

	linkID, err := uuid.Parse(parts[1])
	if err != nil {
		cb.Answer("Invalid link ID", &telegram.CallbackOptions{Alert: true})
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var linksJSON []byte
	var sent bool
	err = db.Pool.QueryRow(ctx, `SELECT links, sent FROM vertex_links WHERE id = $1`, linkID).Scan(&linksJSON, &sent)
	if err != nil {
		cb.Answer("Links not found", &telegram.CallbackOptions{Alert: true})
		return nil
	}

	if sent {
		cb.Answer("Links already sent", &telegram.CallbackOptions{Alert: true})
		return nil
	}

	var links []models.GroundingLink
	if err := json.Unmarshal(linksJSON, &links); err != nil {
		cb.Answer("Links not found", &telegram.CallbackOptions{Alert: true})
		return nil
	}

	var sb strings.Builder
	sb.WriteString("🔗 Grounded Links:\n\n")
	for i, link := range links {
		sb.WriteString(fmt.Sprintf("%d. %s\n%s\n\n", i+1, link.Title, link.URI))
	}

	botClient.SendMessage(cb.ChatID, sb.String(), nil)

	_, _ = db.Pool.Exec(ctx, `UPDATE vertex_links SET sent = true WHERE id = $1`, linkID)

	cb.Answer("Links sent!", nil)
	return nil
}
