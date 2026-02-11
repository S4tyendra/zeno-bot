package aichat

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
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

	doc := models.VertexLinks{
		Links: links,
		Sent:  false,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := db.Collection("vertexlinks").InsertOne(ctx, doc)
	if err != nil {
		return "", err
	}

	return result.InsertedID.(primitive.ObjectID).Hex(), nil
}

func handleGetVertexLinks(cb *telegram.CallbackQuery) error {
	data := string(cb.Data)
	log.Printf("[AiChat] Callback received: %s from user %d", data, cb.Sender.ID)
	parts := strings.Split(data, "|")
	if len(parts) != 2 {
		cb.Answer("Invalid request", &telegram.CallbackOptions{Alert: true})
		return nil
	}

	linkID := parts[1]
	objID, err := primitive.ObjectIDFromHex(linkID)
	if err != nil {
		cb.Answer("Invalid link ID", &telegram.CallbackOptions{Alert: true})
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var doc models.VertexLinks
	err = db.Collection("vertexlinks").FindOne(ctx, bson.M{"_id": objID}).Decode(&doc)
	if err != nil {
		cb.Answer("Links not found", &telegram.CallbackOptions{Alert: true})
		return nil
	}

	if doc.Sent {
		cb.Answer("Links already sent", &telegram.CallbackOptions{Alert: true})
		return nil
	}

	var sb strings.Builder
	sb.WriteString("🔗 Grounded Links:\n\n")
	for i, link := range doc.Links {
		sb.WriteString(fmt.Sprintf("%d. %s\n%s\n\n", i+1, link.Title, link.URI))
	}

	botClient.SendMessage(cb.ChatID, sb.String(), nil)

	db.Collection("vertexlinks").UpdateOne(ctx, bson.M{"_id": objID}, bson.M{"$set": bson.M{"sent": true}})

	cb.Answer("Links sent!", nil)
	return nil
}
