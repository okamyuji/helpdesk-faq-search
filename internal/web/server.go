// Package web serves the search page.
package web

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"slices"
	"sort"
	"time"

	"github.com/okamyuji/helpdesk-faq-search/internal/search"
)

//go:embed templates/search.html
var files embed.FS

var page = template.Must(template.ParseFS(files, "templates/search.html"))

const (
	fullAnswers = 3   // the top answers are shown whole so the user can judge them (ADR 0011)
	excerpt     = 200 // runes of the other answers
)

// Searcher runs one search; main wires it to search.Run.
type Searcher func(ctx context.Context, method, q string) (search.Result, error)

// Server renders the search page.
type Server struct {
	Search      Searcher
	Model       string
	HelpdeskURL string
}

type item struct {
	Question        string
	Answer          string
	Agency, URL     string
	Full, Truncated bool
}

type view struct {
	Query, Method, Model, HelpdeskURL string
	Methods                           []string
	Items                             []item
	Searched, RerankFailed            bool
	Timings                           []string
	Error                             string
}

// Handler returns the routes: / for search and /healthz for the load balancer and ECS.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.searchPage)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprintln(w, "ok") })
	return mux
}

func (s *Server) searchPage(w http.ResponseWriter, r *http.Request) {
	v := view{Query: search.Clean(r.FormValue("q")), Method: r.FormValue("method"), Model: s.Model, HelpdeskURL: s.HelpdeskURL, Methods: search.Methods}
	if v.Method == "" {
		v.Method = "rerank"
	}
	if !slices.Contains(search.Methods, v.Method) {
		http.Error(w, "unknown method", http.StatusBadRequest)
		return
	}
	status := http.StatusOK
	if v.Query != "" {
		res, err := s.Search(r.Context(), v.Method, v.Query)
		if err != nil {
			// The cause stays in the log; the page must not show hosts or SQL.
			log.Printf("search %q: %v", v.Method, err)
			status, v.Error = http.StatusServiceUnavailable, "検索できませんでした。時間をおいてもう一度お試しください。"
		}
		v.Searched, v.RerankFailed, v.Items, v.Timings = true, res.Errors["rerank"] != nil, items(res.Hits), timings(res.Stages)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := page.Execute(w, v); err != nil {
		log.Printf("render: %v", err)
	}
}

func items(hits []search.Hit) []item {
	out := make([]item, len(hits))
	for i, h := range hits {
		it := item{Question: h.FAQ.Question, Answer: h.FAQ.Answer, Agency: h.FAQ.Agency, URL: h.FAQ.URL, Full: i < fullAnswers}
		if r := []rune(it.Answer); !it.Full && len(r) > excerpt {
			it.Answer, it.Truncated = string(r[:excerpt]), true
		}
		out[i] = it
	}
	return out
}

func timings(stages map[string]time.Duration) []string {
	out := make([]string, 0, len(stages))
	for name, d := range stages {
		out = append(out, fmt.Sprintf("%s %dms", name, d.Milliseconds()))
	}
	sort.Strings(out)
	return out
}
