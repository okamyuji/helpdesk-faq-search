package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/okamyuji/helpdesk-faq-search/internal/embed"
	"github.com/okamyuji/helpdesk-faq-search/internal/eval"
	"github.com/okamyuji/helpdesk-faq-search/internal/rerank"
	"github.com/okamyuji/helpdesk-faq-search/internal/search"
)

// Cohere Rerank 3.5 allows 25 calls a minute in this account (ADR 0012); 2.5s apart stays under it.
const rerankPace = 2500 * time.Millisecond

// shortcut: untested wiring of tested parts (eval.Measure, search.Run); checked by running it against real services.
func evalAll(c cfg, stdout io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	f, err := os.Open(c.Paraphrases)
	if err != nil {
		return err
	}
	ps, err := eval.ReadParaphrases(f)
	_ = f.Close()
	if err != nil {
		return err
	}
	db, err := pgxpool.New(ctx, c.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(c.Region))
	if err != nil {
		return err
	}
	d := search.Deps{DB: db, Embed: &embed.Client{BaseURL: c.TEIURL},
		Rerank: &rerank.Client{API: bedrockagentruntime.NewFromConfig(awsCfg), ModelARN: c.RerankARN, TopN: 10}}
	if err := eval.WriteHeader(stdout, c.Model); err != nil {
		return err
	}
	e1, err := originalQuestions(ctx, db)
	if err != nil {
		return err
	}
	r, err := eval.Measure(ctx, e1, func(ctx context.Context, q string) ([]int, error) {
		return search.Vector(ctx, d, c.Model, "answer_doc", q, 10)
	}, 0)
	if err != nil {
		return err
	}
	if err := eval.WriteLine(stdout, "E1", "vector", r); err != nil {
		return err
	}
	sets := map[string]func(eval.Paraphrase) string{"E2": func(p eval.Paraphrase) string { return p.Colloquial }, "E3": func(p eval.Paraphrase) string { return p.Short }}
	for _, set := range []string{"E2", "E3"} {
		cases := make([]eval.Case, len(ps))
		for i, p := range ps {
			cases[i] = eval.Case{Target: p.ID, Query: sets[set](p)}
		}
		for _, m := range search.Methods {
			pace := time.Duration(0)
			if m == "rerank" {
				pace = rerankPace
			}
			r, err := eval.Measure(ctx, cases, func(ctx context.Context, q string) ([]int, error) {
				res, err := search.Run(ctx, d, m, c.Model, q)
				if err == nil && res.Errors["rerank"] != nil {
					err = res.Errors["rerank"] // a throttled rerank falls back silently in the UI; here it must count as a failure
				}
				ids := make([]int, len(res.Hits))
				for i, h := range res.Hits {
					ids[i] = h.FAQ.ID
				}
				return ids, err
			}, pace)
			if err != nil {
				return err
			}
			if err := eval.WriteLine(stdout, set, m, r); err != nil {
				return err
			}
		}
	}
	return nil
}

// originalQuestions picks the same 1,000 FAQs every run: the question is the query, its own answer is the target.
func originalQuestions(ctx context.Context, db *pgxpool.Pool) ([]eval.Case, error) {
	rows, err := db.Query(ctx, `SELECT faq_id, question FROM faqs ORDER BY md5(faq_id::text) LIMIT 1000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []eval.Case
	for rows.Next() {
		var c eval.Case
		if err := rows.Scan(&c.Target, &c.Query); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
