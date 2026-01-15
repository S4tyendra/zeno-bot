package db

import (
	"context"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"zeno/config"
)

var (
	Client *mongo.Client
	DB     *mongo.Database
)

func Connect() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var err error
	Client, err = mongo.Connect(ctx, options.Client().ApplyURI(config.MongoDBURL))
	if err != nil {
		log.Fatal("Failed to connect to MongoDB:", err)
	}

	if err = Client.Ping(ctx, nil); err != nil {
		log.Fatal("Failed to ping MongoDB:", err)
	}

	DB = Client.Database("zeno")
	log.Println("Connected to MongoDB")
}

func Disconnect() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := Client.Disconnect(ctx); err != nil {
		log.Println("Error disconnecting from MongoDB:", err)
	}
}

func Collection(name string) *mongo.Collection {
	return DB.Collection(name)
}
