package repository

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

type BotConfig struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	BotName   string             `bson:"bot_name"`
	BotType   string             `bson:"bot_type"` // "video" or "comment"
	OrgIDs    []string           `bson:"org_id"`
	Sleep     int                `bson:"sleep"` // minutes
	GPMAPI    string             `bson:"gpm_api,omitempty"`
	ProfileID string             `bson:"profile_id,omitempty"`
	Active    bool               `bson:"active"`
	CreatedAt time.Time          `bson:"created_at"`
	UpdatedAt time.Time          `bson:"updated_at"`
}

type BotConfigRepository struct {
	collection *mongo.Collection
	log        *zap.Logger
}

func NewBotConfigRepository(db *mongo.Database, log *zap.Logger) *BotConfigRepository {
	return &BotConfigRepository{
		collection: db.Collection("tiktok_bot_configs"),
		log:        log,
	}
}

func (r *BotConfigRepository) FindByBotName(botName string) (*BotConfig, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var config BotConfig
	filter := bson.M{"bot_name": botName}
	err := r.collection.FindOne(ctx, filter).Decode(&config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

func (r *BotConfigRepository) FindActive() ([]BotConfig, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	filter := bson.M{"active": true}
	cursor, err := r.collection.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var configs []BotConfig
	if err := cursor.All(ctx, &configs); err != nil {
		return nil, err
	}

	return configs, nil
}

func (r *BotConfigRepository) Upsert(config *BotConfig) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	config.UpdatedAt = time.Now()
	if config.CreatedAt.IsZero() {
		config.CreatedAt = time.Now()
	}

	filter := bson.M{"bot_name": config.BotName}
	update := bson.M{"$set": config}
	opts := options.Update().SetUpsert(true)

	_, err := r.collection.UpdateOne(ctx, filter, update, opts)
	return err
}
