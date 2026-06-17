package repository

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"
)

type TiktokConfigManual struct {
	ID         primitive.ObjectID `bson:"_id,omitempty"`
	ProfileIDs []string           `bson:"profile_ids"`
}

type TiktokConfigManualRepository struct {
	collection *mongo.Collection
	log        *zap.Logger
}

func NewTiktokConfigManualRepository(db *mongo.Database, log *zap.Logger) *TiktokConfigManualRepository {
	return &TiktokConfigManualRepository{
		collection: db.Collection("tiktok_configs_manual"),
		log:        log,
	}
}

func (r *TiktokConfigManualRepository) GetProfileIDs() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cursor, err := r.collection.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var seen = make(map[string]bool)
	var profileIDs []string
	for cursor.Next(ctx) {
		var cfg TiktokConfigManual
		if err := cursor.Decode(&cfg); err != nil {
			r.log.Sugar().Warnf("decode tiktok_configs_manual failed: %v", err)
			continue
		}
		for _, id := range cfg.ProfileIDs {
			if !seen[id] {
				seen[id] = true
				profileIDs = append(profileIDs, id)
			}
		}
	}
	return profileIDs, cursor.Err()
}
