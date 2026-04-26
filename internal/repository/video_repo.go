package repository

import (
	"abp-bot-tiktok/internal/models"
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

type VideoDocument struct {
	Keyword     string    `bson:"keyword"`
	VideoID     string    `bson:"video_id"`
	Description string    `bson:"description"`
	PubTime     int64     `bson:"pub_time"`
	UniqueID    string    `bson:"unique_id"`
	AuthID      string    `bson:"auth_id"`
	AuthName    string    `bson:"auth_name"`
	Comments    int64     `bson:"comments"`
	Shares      int64     `bson:"shares"`
	Reactions   int64     `bson:"reactions"`
	Favors      int64     `bson:"favors"`
	Views       int64     `bson:"views"`
	CrawledAt   time.Time `bson:"crawled_at"`
	UpdatedAt   time.Time `bson:"updated_at"`
}

type VideoRepository struct {
	collection *mongo.Collection
	log        *zap.Logger
}

func NewVideoRepository(db *mongo.Database, log *zap.Logger) *VideoRepository {
	collection := db.Collection("tiktok_videos")

	// Tạo index cho video_id (unique) để tránh duplicate
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	indexModel := mongo.IndexModel{
		Keys:    bson.D{{Key: "video_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	}

	_, err := collection.Indexes().CreateOne(ctx, indexModel)
	if err != nil {
		log.Warn("Failed to create index on video_id (may already exist)", zap.Error(err))
	} else {
		log.Info("Index created on tiktok_videos.video_id")
	}

	return &VideoRepository{
		collection: collection,
		log:        log,
	}
}

func (r *VideoRepository) Upsert(video models.VideoItem) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := time.Now()
	doc := VideoDocument{
		Keyword:     video.Keyword,
		VideoID:     video.VideoID,
		Description: video.Description,
		PubTime:     video.PubTime,
		UniqueID:    video.UniqueID,
		AuthID:      video.AuthID,
		AuthName:    video.AuthName,
		Comments:    video.Comments,
		Shares:      video.Shares,
		Reactions:   video.Reactions,
		Favors:      video.Favors,
		Views:       video.Views,
		CrawledAt:   now,
		UpdatedAt:   now,
	}

	filter := bson.M{"video_id": video.VideoID}
	update := bson.M{
		"$set": doc,
		"$setOnInsert": bson.M{
			"crawled_at": now,
		},
	}
	opts := options.Update().SetUpsert(true)

	_, err := r.collection.UpdateOne(ctx, filter, update, opts)
	return err
}

func (r *VideoRepository) BulkUpsert(videos []models.VideoItem) error {
	if len(videos) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	now := time.Now()
	var operations []mongo.WriteModel

	for _, video := range videos {
		doc := VideoDocument{
			Keyword:     video.Keyword,
			VideoID:     video.VideoID,
			Description: video.Description,
			PubTime:     video.PubTime,
			UniqueID:    video.UniqueID,
			AuthID:      video.AuthID,
			AuthName:    video.AuthName,
			Comments:    video.Comments,
			Shares:      video.Shares,
			Reactions:   video.Reactions,
			Favors:      video.Favors,
			Views:       video.Views,
			CrawledAt:   now,
			UpdatedAt:   now,
		}

		filter := bson.M{"video_id": video.VideoID}
		update := bson.M{
			"$set": doc,
			"$setOnInsert": bson.M{
				"crawled_at": now,
			},
		}

		operation := mongo.NewUpdateOneModel().
			SetFilter(filter).
			SetUpdate(update).
			SetUpsert(true)

		operations = append(operations, operation)
	}

	opts := options.BulkWrite().SetOrdered(false)
	result, err := r.collection.BulkWrite(ctx, operations, opts)
	if err != nil {
		return err
	}

	r.log.Info("Bulk upsert completed",
		zap.Int64("inserted", result.InsertedCount),
		zap.Int64("modified", result.ModifiedCount),
		zap.Int64("upserted", result.UpsertedCount),
	)

	return nil
}

func (r *VideoRepository) FindByKeyword(keyword string, limit int64) ([]VideoDocument, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	filter := bson.M{"keyword": keyword}
	opts := options.Find().
		SetSort(bson.D{{Key: "pub_time", Value: -1}}).
		SetLimit(limit)

	cursor, err := r.collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var videos []VideoDocument
	if err := cursor.All(ctx, &videos); err != nil {
		return nil, err
	}

	return videos, nil
}
