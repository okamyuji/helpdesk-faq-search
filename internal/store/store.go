// Package store keeps FAQs and their embeddings in PostgreSQL.
package store

import (
	"context"
	_ "embed"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/okamyuji/helpdesk-faq-search/internal/ingest"
)

//go:embed schema.sql
var schema string

var dims = map[string]int{"ruri-v3-30m": 256, "ruri-v3-130m": 512, "ruri-v3-310m": 768}

// Fields are the embedding kinds; see ADR 0010 for the prefix each one uses.
var Fields = []string{"qa_doc", "qa_plain", "answer_doc"}

// Dim returns the embedding size of a Ruri v3 model.
func Dim(model string) (int, error) {
	d, ok := dims[model]
	if !ok {
		return 0, fmt.Errorf("unknown model %q", model)
	}
	return d, nil
}

// Migrate creates the tables and one HNSW index per model and field. It is safe to run again.
func Migrate(ctx context.Context, db *pgxpool.Pool) error {
	if _, err := db.Exec(ctx, schema); err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	for model, d := range dims {
		for _, f := range Fields {
			// HNSW needs a fixed dimension, so the index casts the untyped column per model.
			q := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS emb_%s_%s_hnsw ON embeddings USING hnsw ((embedding::vector(%d)) vector_cosine_ops) WHERE model_id = '%s' AND field = '%s'`,
				strings.TrimPrefix(strings.ReplaceAll(model, "-", "_"), "ruri_v3_"), f, d, model, f)
			if _, err := db.Exec(ctx, q); err != nil {
				return fmt.Errorf("index %s %s: %w", model, f, err)
			}
		}
	}
	return nil
}

// UpsertFAQs inserts the FAQs or replaces the stored text of the same IDs.
func UpsertFAQs(ctx context.Context, db *pgxpool.Pool, fs []ingest.FAQ) error {
	b := &pgx.Batch{}
	for _, f := range fs {
		b.Queue(`INSERT INTO faqs (faq_id, question, answer, agency, url, qa_norm) VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (faq_id) DO UPDATE SET question=EXCLUDED.question, answer=EXCLUDED.answer, agency=EXCLUDED.agency, url=EXCLUDED.url, qa_norm=EXCLUDED.qa_norm`,
			f.ID, f.Question, f.Answer, f.Agency, f.URL, f.QANorm)
	}
	return sendInTx(ctx, db, b)
}

// UpsertEmbeddings stores one vector per ID. Nothing is written when any input is invalid.
func UpsertEmbeddings(ctx context.Context, db *pgxpool.Pool, model, field string, ids []int, vecs [][]float32) error {
	d, err := Dim(model)
	if err != nil {
		return err
	}
	if !slices.Contains(Fields, field) {
		return fmt.Errorf("unknown field %q", field)
	}
	if len(ids) != len(vecs) {
		return fmt.Errorf("length mismatch: %d ids, %d vectors", len(ids), len(vecs))
	}
	b := &pgx.Batch{}
	for i, v := range vecs {
		if len(v) != d {
			return fmt.Errorf("faq %d: dimension %d, want %d for %s", ids[i], len(v), d, model)
		}
		b.Queue(`INSERT INTO embeddings (faq_id, field, model_id, embedding) VALUES ($1,$2,$3,$4)
ON CONFLICT (faq_id, field, model_id) DO UPDATE SET embedding=EXCLUDED.embedding`, ids[i], field, model, pgvector.NewVector(v))
	}
	return sendInTx(ctx, db, b)
}

// GetFAQs returns the FAQs that exist among ids, keyed by ID.
func GetFAQs(ctx context.Context, db *pgxpool.Pool, ids []int) (map[int]ingest.FAQ, error) {
	rows, err := db.Query(ctx, `SELECT faq_id, question, answer, agency, url, qa_norm FROM faqs WHERE faq_id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	out := map[int]ingest.FAQ{}
	for rows.Next() {
		var f ingest.FAQ
		if err := rows.Scan(&f.ID, &f.Question, &f.Answer, &f.Agency, &f.URL, &f.QANorm); err != nil {
			return nil, err
		}
		out[f.ID] = f
	}
	return out, rows.Err()
}

func sendInTx(ctx context.Context, db *pgxpool.Pool, b *pgx.Batch) error {
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		return tx.SendBatch(ctx, b).Close()
	})
}
