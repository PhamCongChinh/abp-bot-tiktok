package repository

import (
	"abp-bot-tiktok/internal/models"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

type ArticleRepository struct {
	db    *sql.DB
	table string
	log   *zap.Logger
}

func NewArticleRepository(db *sql.DB, table string, log *zap.Logger) *ArticleRepository {
	return &ArticleRepository{db: db, table: table, log: log}
}

func (r *ArticleRepository) FindRecentByOrgIDs(orgIDs []int) ([]models.Article, error) {
	if len(orgIDs) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(orgIDs))
	args := make([]any, len(orgIDs)+1)
	for i, id := range orgIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	args[len(orgIDs)] = time.Now().Add(-24 * time.Hour)

	query := fmt.Sprintf(`
		SELECT id, org_id, title, url, pub_time, source_name, auth_name, comments, views, reactions
		FROM %s
		WHERE org_id IN (%s)
		  AND pub_time >= $%d
		ORDER BY pub_time DESC
	`, r.table, strings.Join(placeholders, ","), len(orgIDs)+1)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query articles failed: %w", err)
	}
	defer rows.Close()

	var articles []models.Article
	for rows.Next() {
		var a models.Article
		if err := rows.Scan(
			&a.ID, &a.OrgID, &a.Title, &a.URL, &a.PubTime,
			&a.SourceName, &a.AuthName, &a.Comments, &a.Views, &a.Reactions,
		); err != nil {
			r.log.Sugar().Warnf("scan article row failed: %v", err)
			continue
		}
		articles = append(articles, a)
	}
	return articles, rows.Err()
}
