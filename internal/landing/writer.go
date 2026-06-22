package landing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/klauspost/compress/zstd"
	minio "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.uber.org/zap"
)

// LandingItem is the input to Land — one extracted entity from the browser scraper.
type LandingItem struct {
	// SourceID identifies the scraper connector (e.g. "scraper_tiktok").
	SourceID string
	// Platform is the social network (e.g. "tiktok").
	Platform string
	// EntityKind is "content" or "profile".
	EntityKind string
	// SourceRecordID is the platform-native ID for the entity.
	SourceRecordID string
	// RawBytes is the verbatim HTTP response body before any parsing.
	RawBytes []byte
	// FetchedAt is the UTC scrape time.
	FetchedAt time.Time
	// Envelope is the JSON envelope metadata stored alongside the raw record.
	Envelope map[string]any
}

// Writer writes raw scraped payloads to MinIO and records them in raw.record,
// then finalises the corresponding raw.fetch_request row.
type Writer struct {
	pool        *pgxpool.Pool
	minioClient *minio.Client
	bucket      string
	encoder     *zstd.Encoder
	log         *zap.Logger
}

// New creates a Writer. minioEndpoint must not include a scheme prefix.
func New(pool *pgxpool.Pool, minioEndpoint, accessKey, secretKey, bucket string, useSSL bool, log *zap.Logger) (*Writer, error) {
	mc, err := minio.New(minioEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("landing: init minio client: %w", err)
	}

	enc, err := zstd.NewWriter(nil)
	if err != nil {
		return nil, fmt.Errorf("landing: init zstd encoder: %w", err)
	}

	return &Writer{
		pool:        pool,
		minioClient: mc,
		bucket:      bucket,
		encoder:     enc,
		log:         log,
	}, nil
}

// Land writes one scraped item: compresses and uploads to MinIO, then inserts
// into raw.record (ON CONFLICT DO NOTHING for idempotent dedup).
func (w *Writer) Land(ctx context.Context, item LandingItem) error {
	// Step 1: hash raw bytes before any parsing.
	sum := sha256.Sum256(item.RawBytes)
	payloadHash := hex.EncodeToString(sum[:])

	// Step 2: build payload_uri and MinIO object key.
	payloadURI := buildPayloadURI(item)
	minioKey := strings.TrimPrefix(payloadURI, "raw/")

	// Step 3: zstd-compress and PUT to MinIO.
	compressed := w.encoder.EncodeAll(item.RawBytes, nil)
	_, err := w.minioClient.PutObject(ctx, w.bucket, minioKey,
		bytes.NewReader(compressed), int64(len(compressed)),
		minio.PutObjectOptions{ContentType: "application/zstd"},
	)
	if err != nil {
		w.log.Error("minio put failed",
			zap.String("key", minioKey),
			zap.Error(err),
		)
		return fmt.Errorf("landing: minio put %s: %w", minioKey, err)
	}
	w.log.Debug("minio put ok",
		zap.String("key", minioKey),
		zap.Int("raw_bytes", len(item.RawBytes)),
		zap.Int("compressed_bytes", len(compressed)),
	)

	// Step 4: insert into raw.record — silent no-op on duplicate.
	envelopeJSON, err := json.Marshal(item.Envelope)
	if err != nil {
		return fmt.Errorf("landing: marshal envelope: %w", err)
	}

	tag, err := w.pool.Exec(ctx, `
		INSERT INTO raw.record
			(source_id, platform, entity_kind, source_record_id,
			 payload_uri, payload_hash, envelope, fetched_at,
			 confidence, process_status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'landed')
		ON CONFLICT (source_id, source_record_id, payload_hash) DO NOTHING`,
		item.SourceID,
		item.Platform,
		item.EntityKind,
		item.SourceRecordID,
		payloadURI,
		payloadHash,
		envelopeJSON,
		item.FetchedAt.UTC(),
		0.7,
	)
	if err != nil {
		w.log.Error("raw.record insert failed",
			zap.String("source_record_id", item.SourceRecordID),
			zap.Error(err),
		)
		return fmt.Errorf("landing: insert raw.record: %w", err)
	}
	if tag.RowsAffected() == 1 {
		w.log.Debug("raw.record inserted",
			zap.String("entity_kind", item.EntityKind),
			zap.String("source_record_id", item.SourceRecordID),
		)
	} else {
		w.log.Debug("raw.record duplicate skipped",
			zap.String("source_record_id", item.SourceRecordID),
		)
	}

	return nil
}

// Finalize updates the raw.fetch_request row after all items have been processed.
// Pass nil for lastError on full success; pass a non-nil pointer for any failure.
func (w *Writer) Finalize(ctx context.Context, id uuid.UUID, status string, lastError *string, attemptsInc int) error {
	_, err := w.pool.Exec(ctx, `
		UPDATE raw.fetch_request
		SET status=$1, last_error=$2, attempts=attempts+$3
		WHERE id=$4`,
		status,
		lastError,
		attemptsInc,
		id,
	)
	if err != nil {
		w.log.Error("finalize fetch_request failed",
			zap.String("id", id.String()),
			zap.String("status", status),
			zap.Error(err),
		)
		return fmt.Errorf("landing: update fetch_request %s: %w", id, err)
	}
	if status == "landed" {
		w.log.Info("fetch_request landed", zap.String("id", id.String()))
	} else {
		errStr := ""
		if lastError != nil {
			errStr = *lastError
		}
		w.log.Warn("fetch_request failed",
			zap.String("id", id.String()),
			zap.String("status", status),
			zap.String("error", errStr),
		)
	}
	return nil
}

// buildPayloadURI constructs the canonical storage path for a raw payload.
// Format: raw/{source_id}/{platform}/{entity_kind}/{YYYY}/{MM}/{DD}/{source_record_id}__{YYYYMMDDTHHMMSSZ}.json.zst
// This mirrors kolp/storage/paths.py:build_raw_path() which uses strftime('%Y%m%dT%H%M%SZ').
func buildPayloadURI(item LandingItem) string {
	t := item.FetchedAt.UTC()
	datePart := t.Format("2006/01/02")
	// compact UTC ISO-8601 — matches Python's strftime('%Y%m%dT%H%M%SZ')
	stampPart := t.Format("20060102T150405Z")
	return fmt.Sprintf("raw/%s/%s/%s/%s/%s__%s.json.zst",
		item.SourceID,
		item.Platform,
		item.EntityKind,
		datePart,
		item.SourceRecordID,
		stampPart,
	)
}
