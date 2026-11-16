package eval

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRankIsOneBasedAndZeroWhenAbsent(t *testing.T) {
	if Rank([]int{5, 7, 9}, 7) != 2 || Rank([]int{5, 7, 9}, 1) != 0 || Rank(nil, 1) != 0 {
		t.Fatal("Rank must return the 1-based position or 0")
	}
}

func TestMeasureComputesRecallAndMRR(t *testing.T) {
	// Targets land at ranks 1, 5, 10 and 11, and one is absent: the edges of every cut-off.
	pad := func(n, target int) []int { return append(slices.Repeat([]int{99}, n-1), target) }
	ranked := map[string][]int{"a": pad(1, 1), "b": pad(5, 2), "c": pad(10, 3), "d": pad(11, 4), "e": pad(3, 98)}
	cases := []Case{{1, "a"}, {2, "b"}, {3, "c"}, {4, "d"}, {5, "e"}}
	r, err := Measure(context.Background(), cases, func(_ context.Context, q string) ([]int, error) { return ranked[q], nil }, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := Report{N: 5, R1: 0.2, R5: 0.4, R10: 0.6, MRR: (1 + 1.0/5 + 1.0/10) / 5}
	if r.N != want.N || r.R1 != want.R1 || r.R5 != want.R5 || r.R10 != want.R10 || abs(r.MRR-want.MRR) > 1e-12 {
		t.Fatalf("report = %+v, want %+v", r, want)
	}
}

func TestMeasureCountsFailuresAsMisses(t *testing.T) {
	r, err := Measure(context.Background(), []Case{{1, "a"}, {1, "b"}}, func(_ context.Context, q string) ([]int, error) {
		if q == "b" {
			return nil, errors.New("throttled")
		}
		return []int{1}, nil
	}, 0)
	if err != nil || r.Failures != 1 || r.R1 != 0.5 {
		t.Fatalf("report = %+v, err = %v", r, err)
	}
}

func TestMeasurePacesCalls(t *testing.T) {
	start := time.Now()
	_, _ = Measure(context.Background(), []Case{{1, "a"}, {1, "b"}, {1, "c"}}, func(context.Context, string) ([]int, error) { return nil, nil }, 30*time.Millisecond)
	if d := time.Since(start); d < 60*time.Millisecond {
		t.Fatalf("3 calls took %v, want at least 2 gaps of 30ms", d)
	}
}

func TestMeasureDoesNotWaitBeforeTheFirstCall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := Measure(ctx, []Case{{1, "a"}}, func(context.Context, string) ([]int, error) { return nil, nil }, time.Hour)
	if err != nil {
		t.Fatalf("err = %v; a single case must not wait for the pace", err)
	}
}

func TestPercentiles(t *testing.T) {
	ds := make([]time.Duration, 100)
	for i := range ds {
		ds[i] = time.Duration(100-i) * time.Millisecond
	}
	p50, p95 := percentiles(ds)
	if p50 != 50*time.Millisecond || p95 != 95*time.Millisecond { // nearest-rank: the 50th and 95th of 100
		t.Fatalf("p50 = %v, p95 = %v", p50, p95)
	}
}

func TestReadParaphrases(t *testing.T) {
	ps, err := ReadParaphrases(strings.NewReader("110\t口語\t短い 語\n189\tb\tc\n"))
	if err != nil || len(ps) != 2 || ps[0] != (Paraphrase{110, "口語", "短い 語"}) {
		t.Fatalf("ps = %+v, err = %v", ps, err)
	}
	for _, bad := range []string{"1\ta\tb\nx\ta\tb\n", "1\ta\tb\n1\tonly two\n"} {
		if _, err := ReadParaphrases(strings.NewReader(bad)); err == nil || !strings.Contains(err.Error(), "line 2") {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

func TestWriteReportIncludesAttribution(t *testing.T) {
	var b strings.Builder
	if err := WriteHeader(&b, "ruri-v3-30m"); err != nil {
		t.Fatal(err)
	}
	WriteLine(&b, "E2", "rerank", Report{N: 100, R1: 0.83, P50: 200 * time.Millisecond})
	out := b.String()
	for _, want := range []string{"JaGovFaqs-22k", "CC BY 4.0", "ruri-v3-30m", "E2\trerank\tn=100\tR@1=0.83", "p50=200ms"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestMeasureWithNoCasesReportsZeros(t *testing.T) {
	r, err := Measure(context.Background(), nil, func(context.Context, string) ([]int, error) { return nil, nil }, 0)
	if err != nil || r != (Report{}) {
		t.Fatalf("report = %+v, err = %v; want zeros, not NaN", r, err)
	}
}
