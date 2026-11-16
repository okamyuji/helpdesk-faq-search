// Package search finds the FAQs that answer a query (ADR 0012).
package search

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/okamyuji/helpdesk-faq-search/internal/embed"
	"github.com/okamyuji/helpdesk-faq-search/internal/ingest"
	"github.com/okamyuji/helpdesk-faq-search/internal/store"
	"github.com/okamyuji/helpdesk-faq-search/internal/textnorm"
)

// Methods lists the search methods; "rerank" is the default and the others are for comparison.
var Methods = []string{"rerank", "vector", "keyword", "rrf"}

// ErrUnknownMethod is returned for a method not in Methods.
var ErrUnknownMethod = errors.New("unknown search method")

const (
	// MaxQueryRunes caps the query; longer input is cut, not rejected.
	MaxQueryRunes = 200
	shown         = 10
	candidates    = 30  // what the reranker sees (ADR 0012)
	rrfDepth      = 100 // per list before fusing
	rrfK          = 60
)

// Embedder turns texts into vectors.
type Embedder interface {
	Embed(ctx context.Context, prefix string, texts []string) ([][]float32, error)
}

// Reranker returns indexes into texts, most relevant first.
type Reranker interface {
	Rerank(ctx context.Context, query string, texts []string) ([]int, error)
}

// Deps are the outside services a search uses.
type Deps struct {
	DB     *pgxpool.Pool
	Embed  Embedder
	Rerank Reranker
}

// Hit is one FAQ in the result.
type Hit struct {
	FAQ ingest.FAQ
}

// Result holds the hits, the time each stage took, and failures that were worked around.
type Result struct {
	Hits   []Hit
	Stages map[string]time.Duration
	Errors map[string]error
}

// Clean trims the query and cuts it to MaxQueryRunes.
func Clean(q string) string {
	r := []rune(strings.TrimSpace(q))
	return string(r[:min(len(r), MaxQueryRunes)])
}

// Run searches with method using the embeddings of model.
func Run(ctx context.Context, d Deps, method, model, q string) (Result, error) {
	res := Result{Stages: map[string]time.Duration{}, Errors: map[string]error{}}
	switch method {
	case "rerank", "vector", "keyword", "rrf":
	default:
		return res, fmt.Errorf("%w: %q", ErrUnknownMethod, method)
	}
	q = Clean(q)
	if q == "" {
		return res, nil
	}
	ids, err := rank(ctx, d, &res, method, model, q)
	if err != nil {
		return res, err
	}
	fs, err := store.GetFAQs(ctx, d.DB, ids)
	if err != nil {
		return res, err
	}
	for _, id := range ids {
		res.Hits = append(res.Hits, Hit{FAQ: fs[id]})
	}
	return res, nil
}

func rank(ctx context.Context, d Deps, res *Result, method, model, q string) ([]int, error) {
	switch method {
	case "vector":
		return timed(res, "vector", func() ([]int, error) { return Vector(ctx, d, model, "qa_doc", q, shown) })
	case "keyword":
		return timed(res, "keyword", func() ([]int, error) { return keywordIDs(ctx, d.DB, q, shown) })
	case "rrf":
		kw, err := timed(res, "keyword", func() ([]int, error) { return keywordIDs(ctx, d.DB, q, rrfDepth) })
		if err != nil {
			return nil, err
		}
		vec, err := timed(res, "vector", func() ([]int, error) { return Vector(ctx, d, model, "qa_doc", q, rrfDepth) })
		if err != nil {
			return nil, err
		}
		return fuse(kw, vec), nil
	}
	cand, err := timed(res, "vector", func() ([]int, error) { return Vector(ctx, d, model, "qa_doc", q, candidates) })
	if err != nil {
		return nil, err
	}
	out, err := timed(res, "rerank", func() ([]int, error) { return rerankIDs(ctx, d, q, cand) })
	if err != nil {
		// Bedrock can throttle (25 calls a minute); the vector order is still a useful answer.
		res.Errors["rerank"] = err
		return cand[:min(shown, len(cand))], nil
	}
	return out, nil
}

func timed(res *Result, stage string, f func() ([]int, error)) ([]int, error) {
	start := time.Now()
	ids, err := f()
	res.Stages[stage] += time.Since(start)
	return ids, err
}

// Vector returns the k FAQs whose field embedding is nearest to q. field is qa_doc for search and answer_doc for evaluation.
func Vector(ctx context.Context, d Deps, model, field, q string, k int) ([]int, error) {
	dim, err := store.Dim(model)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(store.Fields, field) {
		return nil, fmt.Errorf("unknown field %q", field)
	}
	vecs, err := d.Embed.Embed(ctx, embed.PrefixQuery, []string{q})
	if err != nil {
		return nil, err
	}
	var ids []int
	err = pgx.BeginFunc(ctx, d.DB, func(tx pgx.Tx) error {
		// HNSW returns at most hnsw.ef_search rows (default 40), whatever the LIMIT.
		if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL hnsw.ef_search = %d", max(k, 40))); err != nil {
			return err
		}
		ids, err = queryIDs(ctx, tx, vectorSQL(dim, field), model, pgvector.NewVector(vecs[0]), k)
		return err
	})
	return ids, err
}

// vectorSQL casts to the model's dimension to match the partial HNSW index; without it PostgreSQL scans every row.
// The field is a literal so the planner can match the partial index; callers pass only values from store.Fields.
func vectorSQL(dim int, field string) string {
	return fmt.Sprintf(`SELECT faq_id FROM embeddings WHERE model_id = $1 AND field = '%s'
ORDER BY embedding::vector(%d) <=> $2::vector(%d) LIMIT $3`, field, dim, dim)
}

func keywordIDs(ctx context.Context, db *pgxpool.Pool, q string, k int) ([]int, error) {
	terms := strings.Fields(textnorm.NFKC(q))
	parts := make([]string, len(terms))
	args := make([]any, len(terms)+1)
	for i, t := range terms {
		// likequery escapes % and _ so they match themselves.
		parts[i] = fmt.Sprintf("(qa_norm LIKE likequery($%d))::int", i+1)
		args[i] = t
	}
	args[len(terms)] = k
	hits := strings.Join(parts, " + ")
	sql := fmt.Sprintf(`SELECT faq_id FROM faqs WHERE %s > 0 ORDER BY %s DESC, faq_id LIMIT $%d`, hits, hits, len(terms)+1)
	return queryIDs(ctx, db, sql, args...)
}

func rerankIDs(ctx context.Context, d Deps, q string, cand []int) ([]int, error) {
	fs, err := store.GetFAQs(ctx, d.DB, cand)
	if err != nil {
		return nil, err
	}
	texts := make([]string, len(cand))
	for i, id := range cand {
		texts[i] = fs[id].Question + "\n" + fs[id].Answer
	}
	order, err := d.Rerank.Rerank(ctx, q, texts)
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, min(shown, len(order)))
	for _, i := range order[:min(shown, len(order))] {
		out = append(out, cand[i])
	}
	return out, nil
}

// fuse merges ranked lists by Reciprocal Rank Fusion and keeps the top `shown`.
func fuse(lists ...[]int) []int {
	score := map[int]float64{}
	for _, l := range lists {
		for rank, id := range l {
			score[id] += 1 / float64(rrfK+rank+1)
		}
	}
	ids := make([]int, 0, len(score))
	for id := range score {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b int) int {
		return cmp.Or(cmp.Compare(score[b], score[a]), cmp.Compare(a, b))
	})
	return ids[:min(shown, len(ids))]
}

func queryIDs(ctx context.Context, db interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, sql string, args ...any) ([]int, error) {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
