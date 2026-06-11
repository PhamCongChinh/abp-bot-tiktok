package database

import (
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
	"go.uber.org/zap"
)

type PostgresDB struct {
	DB  *sql.DB
	log *zap.Logger
}

func NewPostgresDB(uri string, log *zap.Logger) (*PostgresDB, error) {
	db, err := sql.Open("postgres", uri)
	if err != nil {
		return nil, fmt.Errorf("postgres open failed: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("postgres ping failed: %w", err)
	}
	log.Info("Connected to PostgreSQL")
	return &PostgresDB{DB: db, log: log}, nil
}

func (p *PostgresDB) Close() {
	if p.DB != nil {
		p.DB.Close()
	}
}
