package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/okamyuji/helpdesk-faq-search/internal/embed"
	"github.com/okamyuji/helpdesk-faq-search/internal/rerank"
	"github.com/okamyuji/helpdesk-faq-search/internal/search"
	"github.com/okamyuji/helpdesk-faq-search/internal/store"
	"github.com/okamyuji/helpdesk-faq-search/internal/web"
)

type cfg struct {
	DatabaseURL, TEIURL, RerankARN, Model, Addr, Region, HelpdeskURL string
}

func loadConfig(getenv func(string) string) (cfg, error) {
	or := func(k, def string) string {
		if v := getenv(k); v != "" {
			return v
		}
		return def
	}
	c := cfg{DatabaseURL: getenv("DATABASE_URL"), TEIURL: getenv("TEI_EMBED_URL"), RerankARN: getenv("RERANK_MODEL_ARN"),
		Model: or("EMBEDDING_MODEL", "ruri-v3-30m"), Addr: or("LISTEN_ADDR", ":8080"), Region: or("AWS_REGION", "ap-northeast-1"),
		HelpdeskURL: or("HELPDESK_URL", "mailto:helpdesk@example.com")}
	for _, k := range []string{"DATABASE_URL", "TEI_EMBED_URL", "RERANK_MODEL_ARN"} {
		if getenv(k) == "" {
			return c, fmt.Errorf("%s is required", k)
		}
	}
	if _, err := store.Dim(c.Model); err != nil {
		return c, fmt.Errorf("EMBEDDING_MODEL: %w", err)
	}
	return c, nil
}

// shortcut: untested wiring of real clients; Task 10 checks it end to end with docker run.
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
	go func() {
		<-ctx.Done()
		// ECS sends SIGTERM before stopping the task; let in-flight searches finish.
		shut, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	log.Printf("listening on %s (model %s)", c.Addr, c.Model)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
