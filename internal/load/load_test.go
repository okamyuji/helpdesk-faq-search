package load

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/okamyuji/helpdesk-faq-search/internal/embed"
	"github.com/okamyuji/helpdesk-faq-search/internal/ingest"
	"github.com/okamyuji/helpdesk-faq-search/internal/store/pgtest"
)

const model = "ruri-v3-30m"

type fakeEmbed struct {
	got    map[string][]string // prefix -> texts
	failAt int                 // fail on this call (1-based); 0 never fails
	calls  int
}

func (f *fakeEmbed) Embed(_ context.Context, prefix string, texts []string) ([][]float32, error) {
	f.calls++
	if f.calls == f.failAt {
		return nil, errors.New("tei down")
	}
	if f.got == nil {
		f.got = map[string][]string{}
	}
	f.got[prefix] = append(f.got[prefix], texts...)
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = make([]float32, 256)
		out[i][0] = 1
	}
	return out, nil
}

func faqs() []ingest.FAQ {
	return []ingest.FAQ{
		{ID: 0, Question: "q0", Answer: "a0", Agency: "x", URL: "u", QANorm: "q0\na0"},
		{ID: 1, Question: "q1", Answer: "a1", Agency: "x", URL: "u", QANorm: "q1\na1"},
		{ID: 2, Question: "q2", Answer: "a2", Agency: "x", URL: "u", QANorm: "q2\na2"},
	}
}

type counts struct{ FAQs, Embeddings, QADoc, AnswerDoc int }

func rows(t *testing.T, db *pgxpool.Pool) counts {
	t.Helper()
	var c counts
	err := db.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM faqs), (SELECT count(*) FROM embeddings),
(SELECT count(*) FROM embeddings WHERE field='qa_doc'), (SELECT count(*) FROM embeddings WHERE field='answer_doc')`).Scan(&c.FAQs, &c.Embeddings, &c.QADoc, &c.AnswerDoc)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRunStoresFAQsAndTwoEmbeddingsEach(t *testing.T) {
	db := pgtest.Start(t)
	if _, err := Run(context.Background(), db, &fakeEmbed{}, model, faqs()); err != nil {
		t.Fatal(err)
	}
	if got, want := rows(t, db), (counts{FAQs: 3, Embeddings: 6, QADoc: 3, AnswerDoc: 3}); got != want {
		t.Fatalf("rows = %+v, want %+v", got, want)
	}
}

func TestRunSendsQuestionAnswerThenAnswerWithDocumentPrefix(t *testing.T) {
	e := &fakeEmbed{}
	if _, err := Run(context.Background(), pgtest.Start(t), e, model, faqs()); err != nil {
		t.Fatal(err)
	}
	if got := e.got[embed.PrefixDoc]; len(got) != 6 || got[0] != "q0\na0" || got[3] != "a0" {
		t.Fatalf("texts sent with the document prefix = %q", got)
	}
}

func TestRunReportsStats(t *testing.T) {
	st, err := Run(context.Background(), pgtest.Start(t), &fakeEmbed{}, model, faqs())
	if err != nil {
		t.Fatal(err)
	}
	if st.FAQs != 3 || st.Texts != 6 || math.Abs(st.PerSecond-6/st.EmbedTime.Seconds()) > 1e-6*st.PerSecond {
		t.Fatalf("stats = %+v", st)
	}
}

func TestRunIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := pgtest.Start(t)
	for i := 0; i < 2; i++ {
		if _, err := Run(ctx, db, &fakeEmbed{}, model, faqs()); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
	if got := rows(t, db); got.Embeddings != 6 {
		t.Fatalf("rows = %+v, want 6 embeddings", got)
	}
}

func TestRunWritesNothingWhenEmbeddingFails(t *testing.T) {
	ctx := context.Background()
	db := pgtest.Start(t)
	if _, err := Run(ctx, db, &fakeEmbed{failAt: 2}, model, faqs()); err == nil {
		t.Fatal("want the embedding error")
	}
	if got := rows(t, db); got != (counts{}) {
		t.Fatalf("rows = %+v; a failed load must not leave rows", got)
	}
}

func TestOpenReadsFileAndURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.jsonl")
	if err := os.WriteFile(path, []byte("file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("url\n")) }))
	defer srv.Close()
	for src, want := range map[string]string{path: "file", srv.URL: "url"} {
		rc, err := Open(context.Background(), src)
		if err != nil {
			t.Fatal(err)
		}
		b := make([]byte, 16)
		n, _ := rc.Read(b)
		_ = rc.Close()
		if strings.TrimSpace(string(b[:n])) != want {
			t.Errorf("Open(%q) = %q, want %q", src, b[:n], want)
		}
	}
}

func TestOpenFailsOnHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if _, err := Open(context.Background(), srv.URL); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunRollsBackFAQsWhenEmbeddingWriteFails(t *testing.T) {
	db := pgtest.Start(t)
	// The fake returns 256 dimensions, so writing them as the 768-dimension 310m model fails after the FAQs are written.
	if _, err := Run(context.Background(), db, &fakeEmbed{}, "ruri-v3-310m", faqs()); err == nil {
		t.Fatal("want the dimension error")
	}
	if got := rows(t, db); got != (counts{}) {
		t.Fatalf("rows = %+v; the FAQ rows must be rolled back too", got)
	}
}

func TestRunReportsFirstEmbeddingFailure(t *testing.T) {
	_, err := Run(context.Background(), pgtest.Start(t), &fakeEmbed{failAt: 1}, model, faqs())
	if err == nil || !strings.Contains(err.Error(), "qa_doc") {
		t.Fatalf("err = %v", err)
	}
}
