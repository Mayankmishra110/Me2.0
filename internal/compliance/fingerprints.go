package compliance

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
)

// FingerprintStore persists G2 embeddings in script_fingerprints.
type FingerprintStore interface {
	// Save upserts a fingerprint row. model is the embedding model that
	// produced embedding (e.g. "nomic-embed-text", "gemini-embedding-001") —
	// stored alongside the vector so later comparisons never mix models
	// (CONTEXT D25 / M2-124).
	Save(ctx context.Context, contentID string, shinglesHash, embedding []byte, model string) error
	// ListEmbeddings returns recent channel embeddings (most recent content
	// first), each tagged with the model that produced it. Callers (G2) must
	// only compare vectors sharing the same model.
	ListEmbeddings(ctx context.Context, channelID string, limit int, excludeContentID string) ([]PriorEmbedding, error)
	ListTitles(ctx context.Context, channelID string, limit int, excludeContentID string) ([]string, error)
}

// SQLFingerprintStore uses the script_fingerprints + content_items tables.
type SQLFingerprintStore struct {
	DB *sql.DB
}

// Save upserts a fingerprint row, including the embedding model id.
func (s *SQLFingerprintStore) Save(ctx context.Context, contentID string, shinglesHash, embedding []byte, model string) error {
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO script_fingerprints (content_id, shingles_hash, embedding, embed_model)
VALUES (?, ?, ?, ?)
ON CONFLICT(content_id) DO UPDATE SET
  shingles_hash = excluded.shingles_hash,
  embedding = excluded.embedding,
  embed_model = excluded.embed_model
`, contentID, shinglesHash, embedding, model)
	if err != nil {
		return fmt.Errorf("compliance: save fingerprint %s: %w", contentID, err)
	}
	return nil
}

// ListEmbeddings returns recent channel embeddings (most recent content
// first) tagged with the embed_model that produced each one. G1-M2-124: the
// model tag is what lets G2 skip vectors from a different embedding model
// instead of comparing incompatible vectors.
func (s *SQLFingerprintStore) ListEmbeddings(ctx context.Context, channelID string, limit int, excludeContentID string) ([]PriorEmbedding, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT f.embedding, f.embed_model
FROM script_fingerprints f
JOIN content_items c ON c.id = f.content_id
WHERE c.channel_id = ? AND f.embedding IS NOT NULL AND f.content_id != ?
ORDER BY c.created_at DESC
LIMIT ?
`, channelID, excludeContentID, limit)
	if err != nil {
		return nil, fmt.Errorf("compliance: list embeddings: %w", err)
	}
	defer rows.Close()
	var out []PriorEmbedding
	for rows.Next() {
		var blob []byte
		var model string
		if err := rows.Scan(&blob, &model); err != nil {
			return nil, err
		}
		vec, err := DecodeEmbedding(blob)
		if err != nil {
			continue
		}
		out = append(out, PriorEmbedding{Vector: vec, Model: model})
	}
	return out, rows.Err()
}

// ListTitles returns recent titles from content_items.script JSON.
func (s *SQLFingerprintStore) ListTitles(ctx context.Context, channelID string, limit int, excludeContentID string) ([]string, error) {
	if limit <= 0 {
		limit = 30
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT COALESCE(json_extract(script, '$.long.title'), json_extract(script, '$.short.title'), '')
FROM content_items
WHERE channel_id = ? AND id != ? AND script IS NOT NULL
ORDER BY created_at DESC
LIMIT ?
`, channelID, excludeContentID, limit)
	if err != nil {
		return nil, fmt.Errorf("compliance: list titles: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		if t != "" {
			out = append(out, t)
		}
	}
	return out, rows.Err()
}

// EncodeEmbedding packs float64 vectors as little-endian float32 bytes.
func EncodeEmbedding(v []float64) []byte {
	buf := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(float32(x)))
	}
	return buf
}

// DecodeEmbedding unpacks little-endian float32 bytes to float64.
func DecodeEmbedding(b []byte) ([]float64, error) {
	if len(b)%4 != 0 {
		return nil, fmt.Errorf("embedding blob length %d not divisible by 4", len(b))
	}
	n := len(b) / 4
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:])))
	}
	return out, nil
}
