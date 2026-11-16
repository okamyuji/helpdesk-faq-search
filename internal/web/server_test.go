package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/okamyuji/helpdesk-faq-search/internal/ingest"
	"github.com/okamyuji/helpdesk-faq-search/internal/search"
)

type call struct{ method, q string }

func fakeSearch(calls *[]call, res search.Result, err error) Searcher {
	return func(_ context.Context, method, q string) (search.Result, error) {
		*calls = append(*calls, call{method, q})
		return res, err
	}
}

func hits(n int) search.Result {
	r := search.Result{Stages: map[string]time.Duration{"vector": 4 * time.Millisecond, "rerank": 120 * time.Millisecond}, Errors: map[string]error{}}
	for i := 0; i < n; i++ {
		r.Hits = append(r.Hits, search.Hit{FAQ: ingest.FAQ{ID: i, Question: fmt.Sprintf("質問%d", i),
			Answer: fmt.Sprintf("回答%d", i) + strings.Repeat("あ", 300), Agency: fmt.Sprintf("架空の省%d", i), URL: fmt.Sprintf("https://example.com/faq/%d", i)}})
	}
	return r
}

func get(t *testing.T, s *Server, target string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	body, _ := io.ReadAll(rec.Body)
	return rec.Code, string(body)
}

func server(fn Searcher) *Server {
	return &Server{Search: fn, Model: "ruri-v3-30m", HelpdeskURL: "mailto:helpdesk@example.com"}
}

func TestSearchPageShowsAttribution(t *testing.T) {
	var calls []call
	code, body := get(t, server(fakeSearch(&calls, hits(2), nil)), "/?q=パスワード")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	for _, want := range []string{"架空の省0", `href="https://example.com/faq/0"`, "架空の省1", `href="https://example.com/faq/1"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q", want)
		}
	}
	if strings.Contains(body, "並べ替えに失敗") {
		t.Error("the fallback notice must not appear when reranking worked")
	}
	if len(calls) != 1 || calls[0] != (call{"rerank", "パスワード"}) {
		t.Fatalf("calls = %v, want the default method rerank", calls)
	}
}

func TestSearchShowsFullAnswerForTop3(t *testing.T) {
	var calls []call
	_, body := get(t, server(fakeSearch(&calls, hits(5), nil)), "/?q=x")
	full := "回答2" + strings.Repeat("あ", 300)
	if !strings.Contains(body, full) {
		t.Error("3rd answer must be shown in full")
	}
	if strings.Contains(body, "回答3"+strings.Repeat("あ", 300)) {
		t.Error("4th answer must be cut")
	}
	if !strings.Contains(body, "回答3"+strings.Repeat("あ", 200-len([]rune("回答3")))+"…") {
		t.Error("4th answer must show its first 200 characters and an ellipsis")
	}
}

func TestAnswerOfExactly200RunesIsNotMarkedCut(t *testing.T) {
	var calls []call
	r := hits(4)
	r.Hits[3].FAQ.Answer = strings.Repeat("い", 200)
	_, body := get(t, server(fakeSearch(&calls, r, nil)), "/?q=x")
	if !strings.Contains(body, strings.Repeat("い", 200)+"</div>") {
		t.Error("an answer of exactly 200 characters must be shown whole, without an ellipsis")
	}
}

func TestSearchShowsHelpdeskLink(t *testing.T) {
	var calls []call
	_, body := get(t, server(fakeSearch(&calls, hits(3), nil)), "/?q=x")
	if !strings.Contains(body, `href="mailto:helpdesk@example.com"`) {
		t.Error("body lacks the helpdesk link")
	}
}

func TestSearchEmptyQueryRendersFormOnly(t *testing.T) {
	var calls []call
	code, body := get(t, server(fakeSearch(&calls, hits(3), nil)), "/?q=%20%20")
	if code != http.StatusOK || len(calls) != 0 || !strings.Contains(body, `name="q"`) || strings.Contains(body, "質問0") {
		t.Fatalf("status = %d, calls = %d; want the form only", code, len(calls))
	}
}

func TestSearchLongQueryIsTruncated(t *testing.T) {
	var calls []call
	code, _ := get(t, server(fakeSearch(&calls, hits(1), nil)), "/?q="+url.QueryEscape(strings.Repeat("あ", 10_000)))
	if code != http.StatusOK || len(calls) != 1 || len([]rune(calls[0].q)) != search.MaxQueryRunes {
		t.Fatalf("status = %d, query runes = %d", code, len([]rune(calls[0].q)))
	}
}

func TestSearchUnknownMethodReturns400(t *testing.T) {
	var calls []call
	code, _ := get(t, server(fakeSearch(&calls, hits(1), nil)), "/?q=x&method=bm25")
	if code != http.StatusBadRequest || len(calls) != 0 {
		t.Fatalf("status = %d, calls = %d", code, len(calls))
	}
}

func TestSearchShowsStageFailure(t *testing.T) {
	var calls []call
	r := hits(1)
	r.Errors["rerank"] = errors.New("ThrottlingException")
	code, body := get(t, server(fakeSearch(&calls, r, nil)), "/?q=x")
	if code != http.StatusOK || !strings.Contains(body, "並べ替えに失敗したため") {
		t.Fatalf("status = %d; body lacks the fallback notice", code)
	}
}

func TestSearchErrorReturns503WithoutDetails(t *testing.T) {
	var calls []call
	code, body := get(t, server(fakeSearch(&calls, search.Result{}, errors.New("dial tcp 10.0.0.5:5432: refused"))), "/?q=x")
	if code != http.StatusServiceUnavailable || strings.Contains(body, "10.0.0.5") {
		t.Fatalf("status = %d; internal details must not leak", code)
	}
}

func TestPageShowsModelAndTimings(t *testing.T) {
	var calls []call
	_, body := get(t, server(fakeSearch(&calls, hits(1), nil)), "/?q=x")
	for _, want := range []string{"ruri-v3-30m", "rerank 120ms", "vector 4ms"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q", want)
		}
	}
}

func TestQueryIsEscaped(t *testing.T) {
	var calls []call
	_, body := get(t, server(fakeSearch(&calls, hits(0), nil)), "/?q="+url.QueryEscape(`"><script>alert(1)</script>`))
	if strings.Contains(body, "<script>alert(1)") {
		t.Fatal("query must be HTML-escaped")
	}
}

func TestHealthz(t *testing.T) {
	code, body := get(t, server(nil), "/healthz")
	if code != http.StatusOK || strings.TrimSpace(body) != "ok" {
		t.Fatalf("status = %d, body = %q", code, body)
	}
}

func TestUnknownPathReturns404(t *testing.T) {
	if code, _ := get(t, server(nil), "/admin"); code != http.StatusNotFound {
		t.Fatalf("status = %d", code)
	}
}

func TestSuccessfulRenderLogsNothing(t *testing.T) {
	var buf strings.Builder
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	var calls []call
	get(t, server(fakeSearch(&calls, hits(1), nil)), "/?q=x")
	if buf.Len() != 0 {
		t.Fatalf("log = %q, want nothing", buf.String())
	}
}
