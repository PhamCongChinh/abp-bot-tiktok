package repository

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"
)

type Keyword struct {
	ID      primitive.ObjectID `bson:"_id,omitempty"`
	Keyword string             `bson:"keyword"`
	OrgID   int                `bson:"org_id"`
	Active  bool               `bson:"active"`
}

type KeywordRepository struct {
	collection *mongo.Collection
	log        *zap.Logger
}

func NewKeywordRepository(db *mongo.Database, log *zap.Logger) *KeywordRepository {
	return &KeywordRepository{
		collection: db.Collection("keyword"),
		log:        log,
	}
}

func (r *KeywordRepository) FindByOrgIDs(orgIDs []int) ([]Keyword, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	filter := bson.M{"org_id": bson.M{"$in": orgIDs}}
	cursor, err := r.collection.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var keywords []Keyword
	if err := cursor.All(ctx, &keywords); err != nil {
		return nil, err
	}

	return keywords, nil
}

func (r *KeywordRepository) FindActive() ([]Keyword, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	filter := bson.M{"active": true}
	cursor, err := r.collection.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var keywords []Keyword
	if err := cursor.All(ctx, &keywords); err != nil {
		return nil, err
	}

	return keywords, nil
}
