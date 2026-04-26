package database

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

type MongoDB struct {
	client *mongo.Client
	db     *mongo.Database
	log    *zap.Logger
}

func NewMongoDB(uri, dbName string, log *zap.Logger) (*MongoDB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	clientOpts := options.Client().ApplyURI(uri)
	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, err
	}

	// Ping to verify connection
	if err := client.Ping(ctx, nil); err != nil {
		return nil, err
	}

	log.Info("MongoDB connected", zap.String("database", dbName))

	return &MongoDB{
		client: client,
		db:     client.Database(dbName),
		log:    log,
	}, nil
}

func (m *MongoDB) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.client.Disconnect(ctx)
}

func (m *MongoDB) Database() *mongo.Database {
	return m.db
}

func (m *MongoDB) Collection(name string) *mongo.Collection {
	return m.db.Collection(name)
}
