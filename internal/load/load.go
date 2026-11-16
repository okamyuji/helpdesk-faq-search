// Package load embeds the FAQs and writes them to PostgreSQL.
package load

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/okamyuji/helpdesk-faq-search/internal/embed"
	"github.com/okamyuji/helpdesk-faq-search/internal/ingest"
	"github.com/okamyuji/helpdesk-faq-search/internal/store"
)

// DefaultSource is JaGovFaqs-22k pinned to one commit, so every load reads the same 22,794 rows.
const DefaultSource = "https://huggingface.co/datasets/matsuxr/JaGovFaqs-22k/resolve/a9fd35ae0f7d984cef7c054cfa6ae68bc7b5d354/data.jsonl"

// Embedder turns texts into vectors.
type Embedder interface {
	Embed(ctx context.Context, prefix string, texts []string) ([][]float32, error)
}

// Stats describes one load. PerSecond counts embedded texts per second of embedding time.
type Stats struct {
	FAQs, Texts int
	EmbedTime   time.Duration
	PerSecond   float64
}

// Run stores the FAQs with their qa_doc and answer_doc embeddings for model.
// qa_plain is not computed: no feature uses it yet (ADR 0010).
// Embedding happens first and all rows are written in one transaction, so a failure leaves nothing behind.
func Run(ctx context.Context, db *pgxpool.Pool, e Embedder, model string, fs []ingest.FAQ) (Stats, error) {
	if err := store.Migrate(ctx, db); err != nil {
		return Stats{}, err
	}
	ids := make([]int, len(fs))
	qa := make([]string, len(fs))
	ans := make([]string, len(fs))
	for i, f := range fs {
		ids[i], qa[i], ans[i] = f.ID, f.Question+"\n"+f.Answer, f.Answer
	}
	start := time.Now()
	qaVecs, err := e.Embed(ctx, embed.PrefixDoc, qa)
	if err != nil {
		return Stats{}, fmt.Errorf("embed qa_doc: %w", err)
	}
	ansVecs, err := e.Embed(ctx, embed.PrefixDoc, ans)
	if err != nil {
		return Stats{}, fmt.Errorf("embed answer_doc: %w", err)
	}
	took := time.Since(start)
	err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		if err := store.UpsertFAQs(ctx, tx, fs); err != nil {
			return err
		}
		if err := store.UpsertEmbeddings(ctx, tx, model, "qa_doc", ids, qaVecs); err != nil {
			return err
		}
		return store.UpsertEmbeddings(ctx, tx, model, "answer_doc", ids, ansVecs)
	})
	if err != nil {
		return Stats{}, fmt.Errorf("write: %w", err)
	}
	texts := 2 * len(fs)
	return Stats{FAQs: len(fs), Texts: texts, EmbedTime: took, PerSecond: float64(texts) / max(took.Seconds(), 1e-9)}, nil
}

var httpClient = &http.Client{Timeout: 5 * time.Minute}

// Open reads src, which is an http(s) URL or a local path.
func Open(ctx context.Context, src string) (io.ReadCloser, error) {
	if !strings.HasPrefix(src, "http://") && !strings.HasPrefix(src, "https://") {
		return os.Open(src) //nolint:gosec // the operator chooses the file to load
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, err
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		return nil, fmt.Errorf("get %s: status %d", src, res.StatusCode)
	}
	return res.Body, nil
}
