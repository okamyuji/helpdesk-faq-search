package search

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/okamyuji/helpdesk-faq-search/internal/ingest"
	"github.com/okamyuji/helpdesk-faq-search/internal/store"
	"github.com/okamyuji/helpdesk-faq-search/internal/store/pgtest"
	"github.com/okamyuji/helpdesk-faq-search/internal/textnorm"
)

const model = "ruri-v3-30m"

// at returns a unit vector whose cosine distance from [1,0,...] grows with deg.
func at(deg float64) []float32 {
	v := make([]float32, 256)
	r := deg * math.Pi / 180
	v[0], v[1] = float32(math.Cos(r)), float32(math.Sin(r))
	return v
}

type fakeEmbed struct{ calls int }

func (f *fakeEmbed) Embed(_ context.Context, _ string, texts []string) ([][]float32, error) {
	f.calls++
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = at(0)
	}
	return out, nil
}

type fakeRerank struct {
	calls int
	got   []string
	err   error
}

// Rerank puts the texts in reverse order, so the result differs from the vector order.
func (f *fakeRerank) Rerank(_ context.Context, _ string, texts []string) ([]int, error) {
	f.calls++
	f.got = texts
	if f.err != nil {
		return nil, f.err
	}
	idx := make([]int, 0, len(texts))
	for i := len(texts) - 1; i >= 0; i-- {
		idx = append(idx, i)
	}
	return idx[:min(10, len(idx))], nil
}

// seed stores n FAQs; FAQ i lies i degrees away from the query vector.
func seed(t *testing.T, n int, text func(i int) (q, a string)) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	db := pgtest.Start(t)
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	fs := make([]ingest.FAQ, n)
	ids := make([]int, n)
	vecs := make([][]float32, n)
	for i := range fs {
		q, a := text(i)
		fs[i] = ingest.FAQ{ID: i, Question: q, Answer: a, Agency: "架空の省", URL: "https://example.com", QANorm: textnorm.NFKC(q + "\n" + a)}
		ids[i], vecs[i] = i, at(float64(i))
	}
	if err := store.UpsertFAQs(ctx, db, fs); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertEmbeddings(ctx, db, model, "qa_doc", ids, vecs); err != nil {
		t.Fatal(err)
	}
	return db
}

func plain(i int) (string, string) { return "質問" + string(rune('A'+i%26)), "回答" }

func ids(r Result) []int {
	out := make([]int, len(r.Hits))
	for i, h := range r.Hits {
		out[i] = h.FAQ.ID
	}
	return out
}

func TestVectorReturnsTop10ByDistance(t *testing.T) {
	db := seed(t, 40, plain)
	r, err := Run(context.Background(), Deps{DB: db, Embed: &fakeEmbed{}, Rerank: &fakeRerank{}}, "vector", model, "q")
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}; !slices.Equal(ids(r), want) {
		t.Fatalf("ids = %v, want %v", ids(r), want)
	}
}

// forbidSeqScan makes every new connection of db avoid sequential scans; the tables are tiny, so the planner would scan them.
func forbidSeqScan(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	if _, err := db.Exec(context.Background(), "DO $$BEGIN EXECUTE format('ALTER DATABASE %I SET enable_seqscan = off', current_database()); END$$"); err != nil {
		t.Fatal(err)
	}
	db.Reset()
}

// The cached statement may switch to a generic plan after five runs; that plan must still match the partial index.
func TestVectorQueryUsesHNSWIndexInGenericPlan(t *testing.T) {
	ctx := context.Background()
	db := seed(t, 40, plain)
	forbidSeqScan(t, db)
	conn, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	for _, q := range []string{"SET plan_cache_mode = force_generic_plan", "PREPARE v AS " + vectorSQL(model, 256, "qa_doc")} {
		if _, err := conn.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var plan strings.Builder
	// EXPLAIN takes no bind parameters, so the vector goes in as text.
	rows, err := conn.Query(ctx, fmt.Sprintf("EXPLAIN EXECUTE v('%s', 10)", pgvector.NewVector(at(0)).String()))
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "emb_30m_qa_doc_hnsw") {
		t.Fatalf("plan does not use the HNSW index:\n%s", plan.String())
	}
}

func TestVectorReturnsMoreThanEfSearchDefault(t *testing.T) {
	db := seed(t, 120, plain)
	// Through the index, HNSW stops at hnsw.ef_search rows; a sequential scan would hide that limit.
	forbidSeqScan(t, db)
	got, err := Vector(context.Background(), Deps{DB: db, Embed: &fakeEmbed{}}, model, "qa_doc", "q", rrfDepth)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != rrfDepth {
		t.Fatalf("got %d ids, want %d (hnsw.ef_search defaults to 40)", len(got), rrfDepth)
	}
}

func TestRerankMethodReordersTop30(t *testing.T) {
	db := seed(t, 40, plain)
	rr := &fakeRerank{}
	r, err := Run(context.Background(), Deps{DB: db, Embed: &fakeEmbed{}, Rerank: rr}, "rerank", model, "q")
	if err != nil {
		t.Fatal(err)
	}
	if len(rr.got) != 30 || !strings.HasPrefix(rr.got[0], "質問A\n") {
		t.Fatalf("reranker got %d texts starting %q, want 30 in vector order as question+answer", len(rr.got), rr.got[0])
	}
	if want := []int{29, 28, 27, 26, 25, 24, 23, 22, 21, 20}; !slices.Equal(ids(r), want) {
		t.Fatalf("ids = %v, want %v", ids(r), want)
	}
	if _, ok := r.Stages["rerank"]; !ok {
		t.Fatalf("stages = %v, want a rerank timing", r.Stages)
	}
}

func TestRerankFallsBackToVectorWhenRerankerFails(t *testing.T) {
	db := seed(t, 40, plain)
	r, err := Run(context.Background(), Deps{DB: db, Embed: &fakeEmbed{}, Rerank: &fakeRerank{err: errors.New("throttled")}}, "rerank", model, "q")
	if err != nil {
		t.Fatal(err)
	}
	if r.Errors["rerank"] == nil {
		t.Fatal("want the reranker error recorded")
	}
	if want := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}; !slices.Equal(ids(r), want) {
		t.Fatalf("ids = %v, want the vector order %v", ids(r), want)
	}
}

func TestKeywordTreatsPercentLiterally(t *testing.T) {
	db := seed(t, 3, func(i int) (string, string) {
		return []string{"手数料は10%ですか", "手数料は10円ですか", "100_000円"}[i], "回答"
	})
	r, err := Run(context.Background(), Deps{DB: db}, "keyword", model, "10%")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(r), []int{0}) {
		t.Fatalf("ids = %v, want only the FAQ that contains 10%%", ids(r))
	}
	r, err = Run(context.Background(), Deps{DB: db}, "keyword", model, "1_0")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(r), []int{}) {
		t.Fatalf("_ must not match any character, got %v", ids(r))
	}
}

func TestKeywordCountsRepeatedTermOnceAndBreaksTiesByID(t *testing.T) {
	db := seed(t, 2, func(i int) (string, string) {
		return []string{"VPNの接続", "パスワードの変更"}[i], "回答"
	})
	// An UPDATE writes a new row version at the end, so a scan meets FAQ 1 first; only ORDER BY faq_id puts 0 first.
	if _, err := db.Exec(context.Background(), "UPDATE faqs SET qa_norm = qa_norm WHERE faq_id = 0"); err != nil {
		t.Fatal(err)
	}
	// Counted three times, the repeated term would put FAQ 1 first; counted once, both score 1 and the lower id wins.
	r, err := Run(context.Background(), Deps{DB: db}, "keyword", model, "パスワード パスワード パスワード VPN")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(r), []int{0, 1}) {
		t.Fatalf("ids = %v, want [0 1] (one point each, tie broken by id)", ids(r))
	}
}

func TestKeywordUsesOnlyTheFirstMaxTerms(t *testing.T) {
	db := seed(t, 1, func(int) (string, string) { return "末尾の語", "回答" })
	q := unrelated(maxTerms) + "末尾"
	r, err := Run(context.Background(), Deps{DB: db}, "keyword", model, q)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(r), []int{}) {
		t.Fatalf("ids = %v, want none: the term after the first %d distinct terms is ignored", ids(r), maxTerms)
	}
	r, err = Run(context.Background(), Deps{DB: db}, "keyword", model, unrelated(maxTerms-1)+"末尾")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(r), []int{0}) {
		t.Fatalf("ids = %v, want [0]: the %dth distinct term is used", ids(r), maxTerms)
	}
}

// unrelated returns n distinct terms that match no FAQ.
func unrelated(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "無関係%d ", i)
	}
	return b.String()
}

func TestKeywordIgnoresNUL(t *testing.T) {
	db := seed(t, 1, func(int) (string, string) { return "VPNの接続", "回答" })
	r, err := Run(context.Background(), Deps{DB: db}, "keyword", model, "VP\x00N")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(r), []int{0}) {
		t.Fatalf("ids = %v, want [0]", ids(r))
	}
}

func TestKeywordMatchesFullWidthAndRanksByTermCount(t *testing.T) {
	db := seed(t, 3, func(i int) (string, string) {
		return []string{"ＰＣのパスワード", "PCの設定", "電話"}[i], "回答"
	})
	r, err := Run(context.Background(), Deps{DB: db}, "keyword", model, "ＰＣ　パスワード")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(r), []int{0, 1}) {
		t.Fatalf("ids = %v, want [0 1]: both terms first, one term second", ids(r))
	}
}

func TestRRFOrdersBySummedReciprocalRank(t *testing.T) {
	// Vector order is 0,1,2,3. Only FAQ 3 matches the keyword, so it ranks 1st there and 4th by vector.
	db := seed(t, 4, func(i int) (string, string) {
		return []string{"一", "二", "三", "キーワード"}[i], "回答"
	})
	r, err := Run(context.Background(), Deps{DB: db, Embed: &fakeEmbed{}}, "rrf", model, "キーワード")
	if err != nil {
		t.Fatal(err)
	}
	// 1/61+1/64 for FAQ 3 beats 1/61 for FAQ 0.
	if want := []int{3, 0, 1, 2}; !slices.Equal(ids(r), want) {
		t.Fatalf("ids = %v, want %v", ids(r), want)
	}
}

func TestFuseUsesK60(t *testing.T) {
	// A is 1st in one list; B is 61st in both. With k=60, 2/121 beats 1/61, so B comes first.
	// Any other offset (k+rank-1, k+rank) flips the order, which pins the formula.
	l1, l2 := []int{1000}, []int{}
	for i := 0; i < 59; i++ {
		l1, l2 = append(l1, 2000+i), append(l2, 3000+i)
	}
	l2 = append(l2, 3999)
	l1, l2 = append(l1, 9999), append(l2, 9999)
	if got := fuse(l1, l2); got[0] != 9999 || got[1] != 1000 {
		t.Fatalf("top two = %v, want [9999 1000]", got[:2])
	}
}

func TestFuseBreaksTiesByID(t *testing.T) {
	if got := fuse([]int{7, 3}, []int{3, 7}); !slices.Equal(got, []int{3, 7}) {
		t.Fatalf("got %v, want [3 7]", got)
	}
}

func TestEmptyQueryReturnsNothingWithoutCalls(t *testing.T) {
	e, rr := &fakeEmbed{}, &fakeRerank{}
	for _, q := range []string{"", "  \t　"} {
		r, err := Run(context.Background(), Deps{Embed: e, Rerank: rr}, "rerank", model, q)
		if err != nil || len(r.Hits) != 0 {
			t.Fatalf("q=%q: %v, %v", q, r, err)
		}
	}
	if e.calls+rr.calls != 0 {
		t.Fatalf("calls = %d, want none", e.calls+rr.calls)
	}
}

func TestUnknownMethodReturnsError(t *testing.T) {
	_, err := Run(context.Background(), Deps{}, "bm25", model, "q")
	if !errors.Is(err, ErrUnknownMethod) {
		t.Fatalf("err = %v", err)
	}
}

func TestLongQueryIsTruncatedTo200Runes(t *testing.T) {
	if got := Clean(strings.Repeat("あ", 10_000)); len([]rune(got)) != MaxQueryRunes {
		t.Fatalf("len = %d, want %d", len([]rune(got)), MaxQueryRunes)
	}
}

func TestVectorRejectsUnknownField(t *testing.T) {
	if _, err := Vector(context.Background(), Deps{Embed: &fakeEmbed{}}, model, "x' OR '1'='1", "q", 10); err == nil {
		t.Fatal("want an error for a field outside store.Fields")
	}
}

// vectorSQL puts the model into the SQL text, so Vector must reject anything store.Dim does not know.
func TestVectorRejectsUnknownModel(t *testing.T) {
	if _, err := Vector(context.Background(), Deps{Embed: &fakeEmbed{}}, "x' OR '1'='1", "qa_doc", "q", 10); err == nil {
		t.Fatal("want an error for a model store.Dim does not know")
	}
}
