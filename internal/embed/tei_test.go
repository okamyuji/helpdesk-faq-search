package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type embedReq struct {
	Inputs    []string `json:"inputs"`
	Normalize bool     `json:"normalize"`
	Truncate  bool     `json:"truncate"`
}

// fakeTEI answers /embed with one vector per input whose first value is the input's length in runes.
func fakeTEI(t *testing.T, got *[]embedReq) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed" || r.Method != http.MethodPost {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		var req embedReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		*got = append(*got, req)
		out := make([][]float32, len(req.Inputs))
		for i, s := range req.Inputs {
			out[i] = []float32{float32(len([]rune(s))), 0}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestEmbedSendsPrefixedInputs(t *testing.T) {
	var got []embedReq
	c := &Client{BaseURL: fakeTEI(t, &got).URL}
	vecs, err := c.Embed(context.Background(), PrefixQuery, []string{"パスワード"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Inputs[0] != "検索クエリ: パスワード" || !got[0].Normalize || !got[0].Truncate {
		t.Fatalf("request = %+v", got)
	}
	if len(vecs) != 1 || vecs[0][0] != float32(len([]rune("検索クエリ: パスワード"))) {
		t.Fatalf("vecs = %v", vecs)
	}
}

func TestEmbedSplitsIntoBatchesAndKeepsOrder(t *testing.T) {
	var got []embedReq
	c := &Client{BaseURL: fakeTEI(t, &got).URL}
	texts := make([]string, 70)
	for i := range texts {
		texts[i] = strings.Repeat("a", i+1)
	}
	vecs, err := c.Embed(context.Background(), PrefixPlain, texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || len(got[0].Inputs) != 32 || len(got[2].Inputs) != 6 {
		t.Fatalf("batches = %d (sizes %d, %d), want 3 of 32, 32, 6", len(got), len(got[0].Inputs), len(got[len(got)-1].Inputs))
	}
	for i, v := range vecs {
		if v[0] != float32(i+1) {
			t.Fatalf("vecs[%d][0] = %v, want %d: order was not kept", i, v[0], i+1)
		}
	}
}

func TestEmbedUsesConfiguredBatchSize(t *testing.T) {
	var got []embedReq
	c := &Client{BaseURL: fakeTEI(t, &got).URL, Batch: 4}
	if _, err := c.Embed(context.Background(), PrefixDoc, make([]string, 9)); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("batches = %d, want 3", len(got))
	}
}

func TestEmbedEmptyInputMakesNoRequest(t *testing.T) {
	var got []embedReq
	c := &Client{BaseURL: fakeTEI(t, &got).URL}
	vecs, err := c.Embed(context.Background(), PrefixQuery, nil)
	if err != nil || len(vecs) != 0 || len(got) != 0 {
		t.Fatalf("vecs = %v, err = %v, requests = %d", vecs, err, len(got))
	}
}

func TestEmbedReturnsErrorOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model overloaded", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	_, err := (&Client{BaseURL: srv.URL}).Embed(context.Background(), PrefixQuery, []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "model overloaded") {
		t.Fatalf("err = %v, want the status and body", err)
	}
}

func TestEmbedRejectsWrongVectorCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[[1,2]]`))
	}))
	defer srv.Close()
	_, err := (&Client{BaseURL: srv.URL}).Embed(context.Background(), PrefixQuery, []string{"a", "b"})
	if err == nil || !strings.Contains(err.Error(), "1 vectors for 2 inputs") {
		t.Fatalf("err = %v", err)
	}
}

func TestEmbedReturnsErrorWhenTEIIsDown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	if _, err := (&Client{BaseURL: url}).Embed(context.Background(), PrefixQuery, []string{"x"}); err == nil {
		t.Fatal("want an error when TEI does not answer")
	}
}

func TestEmbedReturnsErrorOnBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"error":"x"}`))
	}))
	defer srv.Close()
	_, err := (&Client{BaseURL: srv.URL}).Embed(context.Background(), PrefixQuery, []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("err = %v", err)
	}
}
