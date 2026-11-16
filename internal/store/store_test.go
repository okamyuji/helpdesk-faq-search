package store

import (
	"context"
	"strings"
	"testing"

	"github.com/okamyuji/helpdesk-faq-search/internal/ingest"
	"github.com/okamyuji/helpdesk-faq-search/internal/store/pgtest"
)

func faq(id int, q string) ingest.FAQ {
	return ingest.FAQ{ID: id, Question: q, Answer: "a" + q, Agency: "架空の省", URL: "https://example.com/" + q, QANorm: q + "\na" + q}
}

func vec(n int) []float32 {
	v := make([]float32, n)
	v[0] = 1
	return v
}

func TestDim(t *testing.T) {
	for model, want := range map[string]int{"ruri-v3-30m": 256, "ruri-v3-130m": 512, "ruri-v3-310m": 768} {
		if got, err := Dim(model); err != nil || got != want {
			t.Errorf("Dim(%q) = %d, %v; want %d", model, got, err, want)
		}
	}
	if _, err := Dim("ruri-v3-70m"); err == nil {
		t.Error("Dim of an unknown model must fail")
	}
}

func TestMigrateCreatesExtensionsAndIndexes(t *testing.T) {
	ctx := context.Background()
	db := pgtest.Start(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM pg_extension WHERE extname IN ('vector','pg_bigm')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("extensions = %d, %v; want 2", n, err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE tablename='embeddings' AND indexdef LIKE '%hnsw%'`).Scan(&n); err != nil || n != 9 {
		t.Fatalf("hnsw indexes = %d, %v; want 9", n, err)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := pgtest.Start(t)
	for i := 0; i < 2; i++ {
		if err := Migrate(ctx, db); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
}

func TestUpsertFAQsTwiceKeepsOneRowAndLatestText(t *testing.T) {
	ctx := context.Background()
	db := pgtest.Start(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := UpsertFAQs(ctx, db, []ingest.FAQ{faq(0, "古い"), faq(1, "二件目")}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertFAQs(ctx, db, []ingest.FAQ{faq(0, "新しい")}); err != nil {
		t.Fatal(err)
	}
	got, err := GetFAQs(ctx, db, []int{0, 1, 99})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != faq(0, "新しい") || got[1] != faq(1, "二件目") {
		t.Fatalf("GetFAQs = %+v", got)
	}
}

func TestUpsertEmbeddingsStoresAndReplaces(t *testing.T) {
	ctx := context.Background()
	db := pgtest.Start(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := UpsertFAQs(ctx, db, []ingest.FAQ{faq(0, "q")}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := UpsertEmbeddings(ctx, db, "ruri-v3-30m", "qa_doc", []int{0}, [][]float32{vec(256)}); err != nil {
			t.Fatal(err)
		}
	}
	var n, dims int
	if err := db.QueryRow(ctx, `SELECT count(*), max(vector_dims(embedding)) FROM embeddings`).Scan(&n, &dims); err != nil || n != 1 || dims != 256 {
		t.Fatalf("rows = %d, dims = %d, %v; want 1 row of 256", n, dims, err)
	}
}

func TestUpsertEmbeddingsRejectsBadInput(t *testing.T) {
	ctx := context.Background()
	db := pgtest.Start(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := UpsertFAQs(ctx, db, []ingest.FAQ{faq(0, "q")}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, model, field string
		ids                []int
		vecs               [][]float32
		want               string
	}{
		{"wrong dimension", "ruri-v3-30m", "qa_doc", []int{0}, [][]float32{vec(512)}, "dimension 512, want 256"},
		{"unknown field", "ruri-v3-30m", "title", []int{0}, [][]float32{vec(256)}, `unknown field "title"`},
		{"unknown model", "ruri-v3-70m", "qa_doc", []int{0}, [][]float32{vec(256)}, `unknown model "ruri-v3-70m"`},
		{"length mismatch", "ruri-v3-30m", "qa_doc", []int{0, 0}, [][]float32{vec(256)}, "length mismatch"},
	}
	for _, c := range cases {
		err := UpsertEmbeddings(ctx, db, c.model, c.field, c.ids, c.vecs)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to mention %q", c.name, err, c.want)
		}
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM embeddings`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows = %d, %v; rejected input must write nothing", n, err)
	}
}
