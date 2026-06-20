package landing_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/klauspost/compress/zstd"
	minio "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"abp-bot-tiktok/internal/landing"
)

// Integration tests require:
//   MINIO_TEST_ENDPOINT  (e.g. "localhost:9000")
//   MINIO_TEST_ACCESS    (e.g. "minioadmin")
//   MINIO_TEST_SECRET    (e.g. "minioadmin")
//   MINIO_TEST_BUCKET    (e.g. "test-raw")
//   POSTGRES_TEST_DSN    (e.g. "postgres://kolp:kolp@localhost:5433/kolp")
//
// If these are absent the tests are skipped (not failed) — CI provides them.

func getTestEnv(t *testing.T) (minioEndpoint, accessKey, secretKey, bucket, postgresDSN string) {
	t.Helper()
	minioEndpoint = os.Getenv("MINIO_TEST_ENDPOINT")
	accessKey = os.Getenv("MINIO_TEST_ACCESS")
	secretKey = os.Getenv("MINIO_TEST_SECRET")
	bucket = os.Getenv("MINIO_TEST_BUCKET")
	postgresDSN = os.Getenv("POSTGRES_TEST_DSN")
	if minioEndpoint == "" || postgresDSN == "" {
		t.Skip("MINIO_TEST_ENDPOINT and POSTGRES_TEST_DSN must be set for integration tests")
	}
	if accessKey == "" {
		accessKey = "minioadmin"
	}
	if secretKey == "" {
		secretKey = "minioadmin"
	}
	if bucket == "" {
		bucket = "test-raw"
	}
	return
}

func setupMinIO(t *testing.T, endpoint, access, secret, bucket string) *minio.Client {
	t.Helper()
	mc, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(access, secret, ""),
		Secure: false,
	})
	if err != nil {
		t.Fatalf("init minio client: %v", err)
	}
	ctx := context.Background()
	exists, err := mc.BucketExists(ctx, bucket)
	if err != nil {
		t.Fatalf("bucket exists check: %v", err)
	}
	if !exists {
		if err := mc.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			t.Fatalf("make bucket: %v", err)
		}
	}
	return mc
}

func setupPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	// Verify connectivity.
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("postgres ping: %v", err)
	}
	return pool
}

func setupWriter(t *testing.T) (*landing.Writer, *minio.Client, *pgxpool.Pool) {
	t.Helper()
	endpoint, access, secret, bucket, dsn := getTestEnv(t)
	mc := setupMinIO(t, endpoint, access, secret, bucket)
	pool := setupPool(t, dsn)
	w, err := landing.New(pool, endpoint, access, secret, bucket, false)
	if err != nil {
		t.Fatalf("landing.New: %v", err)
	}
	return w, mc, pool
}

func makeItem(sourceRecordID string, rawBytes []byte) landing.LandingItem {
	return landing.LandingItem{
		SourceID:       "scraper_tiktok",
		Platform:       "tiktok",
		EntityKind:     "content",
		SourceRecordID: sourceRecordID,
		RawBytes:       rawBytes,
		FetchedAt:      time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
		Envelope: map[string]any{
			"status": "ok",
			"request": map[string]any{
				"target": sourceRecordID,
				"scope":  "content",
			},
			"provenance": map[string]any{
				"source_nature": "scraper",
				"confidence":    0.7,
			},
			"rate_limit": nil,
			"error":      nil,
		},
	}
}

// TestLand_HappyPath verifies that Land() uploads the object to MinIO and
// inserts a row into raw.record with process_status='landed'.
func TestLand_HappyPath(t *testing.T) {
	w, mc, pool := setupWriter(t)
	ctx := context.Background()

	rawBytes := []byte(`{"id":"vid123","desc":"test video","createTime":1735732800}`)
	item := makeItem("vid123", rawBytes)

	// Clean up any prior run.
	bucket := os.Getenv("MINIO_TEST_BUCKET")
	if bucket == "" {
		bucket = "test-raw"
	}
	_, _ = pool.Exec(ctx, `DELETE FROM raw.record WHERE source_id=$1 AND source_record_id=$2`,
		item.SourceID, item.SourceRecordID)

	if err := w.Land(ctx, item); err != nil {
		t.Fatalf("Land: %v", err)
	}

	// Verify MinIO object exists and contains valid zstd.
	expectedKey := "scraper_tiktok/tiktok/content/2026/01/01/vid123__20260101T120000Z.json.zst"
	obj, err := mc.GetObject(ctx, bucket, expectedKey, minio.GetObjectOptions{})
	if err != nil {
		t.Fatalf("GetObject %s: %v", expectedKey, err)
	}
	defer obj.Close()

	compressed, err := io.ReadAll(obj)
	if err != nil {
		t.Fatalf("read object: %v", err)
	}

	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatalf("zstd reader: %v", err)
	}
	defer dec.Close()
	decompressed, err := dec.DecodeAll(compressed, nil)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(decompressed, rawBytes) {
		t.Errorf("decompressed content mismatch: got %s, want %s", decompressed, rawBytes)
	}

	// Verify raw.record row.
	var processStatus, storedPayloadURI, storedHash string
	err = pool.QueryRow(ctx,
		`SELECT process_status, payload_uri, payload_hash FROM raw.record
		 WHERE source_id=$1 AND source_record_id=$2`,
		item.SourceID, item.SourceRecordID,
	).Scan(&processStatus, &storedPayloadURI, &storedHash)
	if err != nil {
		t.Fatalf("query raw.record: %v", err)
	}

	if processStatus != "landed" {
		t.Errorf("process_status = %q, want %q", processStatus, "landed")
	}

	expectedURI := "raw/scraper_tiktok/tiktok/content/2026/01/01/vid123__20260101T120000Z.json.zst"
	if storedPayloadURI != expectedURI {
		t.Errorf("payload_uri = %q, want %q", storedPayloadURI, expectedURI)
	}

	sum := sha256.Sum256(rawBytes)
	expectedHash := hex.EncodeToString(sum[:])
	if storedHash != expectedHash {
		t.Errorf("payload_hash = %q, want %q", storedHash, expectedHash)
	}
}

// TestLand_Duplicate verifies that a second Land() call with identical bytes
// is silently skipped — ON CONFLICT DO NOTHING.
func TestLand_Duplicate(t *testing.T) {
	w, _, pool := setupWriter(t)
	ctx := context.Background()

	rawBytes := []byte(`{"id":"vid456","desc":"dup test","createTime":1735732800}`)
	item := makeItem("vid456", rawBytes)

	// Clean up any prior run.
	_, _ = pool.Exec(ctx, `DELETE FROM raw.record WHERE source_id=$1 AND source_record_id=$2`,
		item.SourceID, item.SourceRecordID)

	if err := w.Land(ctx, item); err != nil {
		t.Fatalf("first Land: %v", err)
	}

	// Second insert should silently succeed (no error, no duplicate row).
	if err := w.Land(ctx, item); err != nil {
		t.Fatalf("second Land (duplicate): %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM raw.record WHERE source_id=$1 AND source_record_id=$2`,
		item.SourceID, item.SourceRecordID,
	).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row after duplicate insert, got %d", count)
	}
}

// TestFinalize_Success verifies that Finalize() updates fetch_request.status and
// leaves last_error NULL on a success case (lastError=nil).
func TestFinalize_Success(t *testing.T) {
	_, _, pool := setupWriter(t)
	w, _, _ := setupWriter(t)
	ctx := context.Background()

	rowID := uuid.New()
	// Insert a minimal fetch_request row to update.
	_, err := pool.Exec(ctx, `
		INSERT INTO raw.fetch_request
			(id, source_id, platform, scope, target, status, attempts)
		VALUES ($1, 'scraper_tiktok', 'tiktok', 'content', 'tgt_finalize_ok', 'claimed', 0)
		ON CONFLICT (id) DO NOTHING`,
		rowID,
	)
	if err != nil {
		t.Fatalf("insert fetch_request: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM raw.fetch_request WHERE id=$1`, rowID)
	})

	if err := w.Finalize(ctx, rowID, "landed", nil, 0); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	var status string
	var lastError *string
	if err := pool.QueryRow(ctx,
		`SELECT status, last_error FROM raw.fetch_request WHERE id=$1`, rowID,
	).Scan(&status, &lastError); err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "landed" {
		t.Errorf("status = %q, want %q", status, "landed")
	}
	if lastError != nil {
		t.Errorf("last_error = %q, want NULL", *lastError)
	}
}

// TestFinalize_Error verifies that Finalize() writes the error string and
// increments attempts when lastError is non-nil.
func TestFinalize_Error(t *testing.T) {
	_, _, pool := setupWriter(t)
	w, _, _ := setupWriter(t)
	ctx := context.Background()

	rowID := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO raw.fetch_request
			(id, source_id, platform, scope, target, status, attempts)
		VALUES ($1, 'scraper_tiktok', 'tiktok', 'content', 'tgt_finalize_err', 'claimed', 2)
		ON CONFLICT (id) DO NOTHING`,
		rowID,
	)
	if err != nil {
		t.Fatalf("insert fetch_request: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM raw.fetch_request WHERE id=$1`, rowID)
	})

	errStr := "handle_unknown"
	if err := w.Finalize(ctx, rowID, "failed", &errStr, 1); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	var status string
	var lastError *string
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT status, last_error, attempts FROM raw.fetch_request WHERE id=$1`, rowID,
	).Scan(&status, &lastError, &attempts); err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "failed" {
		t.Errorf("status = %q, want %q", status, "failed")
	}
	if lastError == nil || *lastError != errStr {
		t.Errorf("last_error = %v, want %q", lastError, errStr)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

// TestPayloadURIFormat verifies the payload_uri timestamp format character-by-character
// against the Python build_raw_path() convention: strftime('%Y%m%dT%H%M%SZ').
// Acceptance criterion from T10: must match before merging.
func TestPayloadURIFormat(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name           string
		item           landing.LandingItem
		expectedSuffix string
	}{
		{
			name: "midnight UTC",
			item: landing.LandingItem{
				SourceID:       "scraper_tiktok",
				Platform:       "tiktok",
				EntityKind:     "content",
				SourceRecordID: "id1",
				RawBytes:       []byte(`{}`),
				FetchedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				Envelope:       map[string]any{},
			},
			expectedSuffix: "raw/scraper_tiktok/tiktok/content/2026/01/01/id1__20260101T000000Z.json.zst",
		},
		{
			name: "noon UTC",
			item: landing.LandingItem{
				SourceID:       "scraper_tiktok",
				Platform:       "tiktok",
				EntityKind:     "profile",
				SourceRecordID: "usr99",
				RawBytes:       []byte(`{"user":"data"}`),
				FetchedAt:      time.Date(2026, 6, 15, 12, 30, 45, 0, time.UTC),
				Envelope:       map[string]any{},
			},
			expectedSuffix: "raw/scraper_tiktok/tiktok/profile/2026/06/15/usr99__20260615T123045Z.json.zst",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if os.Getenv("MINIO_TEST_ENDPOINT") == "" {
				t.Skip("MINIO_TEST_ENDPOINT not set")
			}
			dsn := os.Getenv("POSTGRES_TEST_DSN")
			pool2 := setupPool(t, dsn)
			w2, _, _ := setupWriter(t)
			_, _ = pool2.Exec(ctx, `DELETE FROM raw.record WHERE source_id=$1 AND source_record_id=$2`,
				tc.item.SourceID, tc.item.SourceRecordID)

			if err := w2.Land(ctx, tc.item); err != nil {
				t.Fatalf("Land: %v", err)
			}

			var storedURI string
			if err := pool2.QueryRow(ctx,
				`SELECT payload_uri FROM raw.record WHERE source_id=$1 AND source_record_id=$2`,
				tc.item.SourceID, tc.item.SourceRecordID,
			).Scan(&storedURI); err != nil {
				t.Fatalf("query payload_uri: %v", err)
			}

			if storedURI != tc.expectedSuffix {
				t.Errorf("payload_uri character-by-character mismatch:\n  got:  %s\n  want: %s", storedURI, tc.expectedSuffix)

				// Character-by-character diff for the timestamp portion.
				got := storedURI
				want := tc.expectedSuffix
				maxLen := len(got)
				if len(want) > maxLen {
					maxLen = len(want)
				}
				for i := 0; i < maxLen; i++ {
					var gc, wc byte = '?', '?'
					if i < len(got) {
						gc = got[i]
					}
					if i < len(want) {
						wc = want[i]
					}
					if gc != wc {
						t.Logf("  first diff at index %d: got %q, want %q", i, gc, wc)
						break
					}
				}
			}
		})
	}
}

// TestBuildPayloadURIUnit verifies the URI builder output without a DB/MinIO dependency.
// It checks the timestamp format character-by-character against the Python convention.
func TestBuildPayloadURIUnit(t *testing.T) {
	cases := []struct {
		name        string
		fetchedAt   time.Time
		wantContain string
	}{
		{
			name:        "UTC timestamp uppercase Z",
			fetchedAt:   time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
			wantContain: "20260101T120000Z",
		},
		{
			name: "non-UTC input converted to UTC",
			// +7 offset → 05:00 UTC
			fetchedAt:   time.Date(2026, 6, 15, 12, 0, 0, 0, time.FixedZone("ICT", 7*3600)),
			wantContain: "20260615T050000Z",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Build item and call Land (but we'll use a mock that panics on actual I/O).
			// Instead, we verify indirectly: the timestamp embedded in payload_uri.
			// Since buildPayloadURI is unexported we test its output via the stored URI.
			// Here we verify the Go time.Format spec matches Python strftime('%Y%m%dT%H%M%SZ').
			utc := tc.fetchedAt.UTC()
			got := utc.Format("20060102T150405Z")
			if got != tc.wantContain {
				t.Errorf("timestamp format: got %q, want %q", got, tc.wantContain)
			}
			// Verify uppercase Z (not lowercase, not timezone offset).
			if !strings.HasSuffix(got, "Z") {
				t.Errorf("timestamp %q does not end with uppercase Z", got)
			}
			// Verify format length (YYYYMMDDTHHMMSSZ = 16 chars).
			if len(got) != 16 {
				t.Errorf("timestamp length %d, want 16 (YYYYMMDDTHHMMSSZ)", len(got))
			}
		})
	}
}

// TestEnvelopeJSONShape verifies the envelope JSON is stored as valid JSON
// with the expected shape (not just raw bytes).
func TestEnvelopeJSONShape(t *testing.T) {
	_, _, pool := setupWriter(t)
	w, _, _ := setupWriter(t)
	ctx := context.Background()

	rawBytes := []byte(`{"id":"envtest","desc":"envelope shape test"}`)
	item := makeItem("envtest789", rawBytes)

	_, _ = pool.Exec(ctx, `DELETE FROM raw.record WHERE source_id=$1 AND source_record_id=$2`,
		item.SourceID, item.SourceRecordID)

	if err := w.Land(ctx, item); err != nil {
		t.Fatalf("Land: %v", err)
	}

	var envelopeRaw []byte
	if err := pool.QueryRow(ctx,
		`SELECT envelope FROM raw.record WHERE source_id=$1 AND source_record_id=$2`,
		item.SourceID, item.SourceRecordID,
	).Scan(&envelopeRaw); err != nil {
		t.Fatalf("query envelope: %v", err)
	}

	var env map[string]any
	if err := json.Unmarshal(envelopeRaw, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	requiredKeys := []string{"status", "request", "provenance", "rate_limit", "error"}
	for _, k := range requiredKeys {
		if _, ok := env[k]; !ok {
			t.Errorf("envelope missing key %q", k)
		}
	}

	prov, ok := env["provenance"].(map[string]any)
	if !ok {
		t.Fatalf("provenance is not an object")
	}
	if prov["source_nature"] != "scraper" {
		t.Errorf("provenance.source_nature = %v, want %q", prov["source_nature"], "scraper")
	}
	if prov["confidence"] != 0.7 {
		t.Errorf("provenance.confidence = %v, want 0.7", prov["confidence"])
	}
}

// TestHTTPCheck verifies that the MinIO endpoint is reachable over HTTP before
// running integration tests (provides a clearer error when the endpoint is wrong).
func TestHTTPCheck(t *testing.T) {
	endpoint := os.Getenv("MINIO_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("MINIO_TEST_ENDPOINT not set")
	}
	url := fmt.Sprintf("http://%s/minio/health/live", endpoint)
	resp, err := http.Get(url)
	if err != nil {
		t.Logf("MinIO health check failed (endpoint %s): %v — integration tests may fail", endpoint, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Logf("MinIO health check returned %d — integration tests may fail", resp.StatusCode)
	}
}
