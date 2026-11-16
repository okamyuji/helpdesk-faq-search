package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/okamyuji/helpdesk-faq-search/internal/embed"
	"github.com/okamyuji/helpdesk-faq-search/internal/ingest"
	"github.com/okamyuji/helpdesk-faq-search/internal/load"
	"github.com/okamyuji/helpdesk-faq-search/internal/rerank"
	"github.com/okamyuji/helpdesk-faq-search/internal/search"
	"github.com/okamyuji/helpdesk-faq-search/internal/store"
	"github.com/okamyuji/helpdesk-faq-search/internal/web"
)

type cfg struct {
	DatabaseURL, TEIURL, RerankARN, Model, Addr, Region, HelpdeskURL, Source, Paraphrases string
}

var (
	serveRequired = []string{"DATABASE_URL", "TEI_EMBED_URL", "RERANK_MODEL_ARN"}
	loadRequired  = []string{"DATABASE_URL", "TEI_EMBED_URL"}
)

func loadConfig(getenv func(string) string, required []string) (cfg, error) {
	or := func(k, def string) string {
		if v := getenv(k); v != "" {
			return v
		}
		return def
	}
	c := cfg{DatabaseURL: getenv("DATABASE_URL"), TEIURL: getenv("TEI_EMBED_URL"), RerankARN: getenv("RERANK_MODEL_ARN"),
		Model: or("EMBEDDING_MODEL", "ruri-v3-30m"), Addr: or("LISTEN_ADDR", ":8080"), Region: or("AWS_REGION", "ap-northeast-1"),
		HelpdeskURL: or("HELPDESK_URL", "mailto:helpdesk@example.com"), Source: or("DATA_SOURCE", load.DefaultSource),
		Paraphrases: or("EVAL_PARAPHRASES", "eval/paraphrases.tsv")}
	for _, k := range required {
		if getenv(k) == "" {
			return c, fmt.Errorf("%s is required", k)
		}
	}
	if _, err := store.Dim(c.Model); err != nil {
		return c, fmt.Errorf("EMBEDDING_MODEL: %w", err)
	}
	return c, nil
}

// shortcut: untested wiring of real clients; the end-to-end run with docker checks it.
func serve(c cfg) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, err := pgxpool.New(ctx, c.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(c.Region))
	if err != nil {
		return err
	}
	deps := search.Deps{DB: db, Embed: &embed.Client{BaseURL: c.TEIURL},
		Rerank: &rerank.Client{API: bedrockagentruntime.NewFromConfig(awsCfg), ModelARN: c.RerankARN, TopN: 10}}
	s := &web.Server{Model: c.Model, HelpdeskURL: c.HelpdeskURL, Search: func(ctx context.Context, method, q string) (search.Result, error) {
		return search.Run(ctx, deps, method, c.Model, q)
	}}
	srv := &http.Server{Addr: c.Addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	shutdown := make(chan error, 1)
	go func() {
		<-ctx.Done()
		// ECS sends SIGTERM before stopping the task; let in-flight searches finish.
		shut, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		shutdown <- srv.Shutdown(shut)
	}()
	log.Printf("listening on %s (model %s)", c.Addr, c.Model)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// ListenAndServe returns as soon as Shutdown starts; wait until in-flight requests are done.
	return <-shutdown
}

// shortcut: untested wiring; the end-to-end run checks it against a real PostgreSQL and TEI.
func loadAll(c cfg, stdout io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	src, err := load.Open(ctx, c.Source)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	fs, err := ingest.ReadJSONL(src)
	if err != nil {
		return err
	}
	db, err := pgxpool.New(ctx, c.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	st, err := load.Run(ctx, db, &embed.Client{BaseURL: c.TEIURL}, c.Model, fs)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "load model=%s faqs=%d texts=%d embed_seconds=%.1f texts_per_second=%.1f\n",
		c.Model, st.FAQs, st.Texts, st.EmbedTime.Seconds(), st.PerSecond)
	return err
}
