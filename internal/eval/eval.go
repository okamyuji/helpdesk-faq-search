// Package eval measures how often the right FAQ comes first, and how long a search takes.
package eval

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Case is one query whose correct answer is the FAQ Target.
type Case struct {
	Target int
	Query  string
}

// Report summarizes one method on one set of cases.
type Report struct {
	N, Failures      int
	R1, R5, R10, MRR float64
	P50, P95         time.Duration
}

// Rank returns the 1-based position of target in ranked, or 0 when it is absent.
func Rank(ranked []int, target int) int {
	return slices.Index(ranked, target) + 1
}

// Measure runs search on every case. A failed call counts as a miss; pace leaves time between calls for rate-limited services.
func Measure(ctx context.Context, cases []Case, search func(context.Context, string) ([]int, error), pace time.Duration) (Report, error) {
	r := Report{N: len(cases)}
	lat := make([]time.Duration, 0, len(cases))
	for i, c := range cases {
		if i > 0 {
			select {
			case <-ctx.Done():
				return r, ctx.Err()
			case <-time.After(pace):
			}
		}
		start := time.Now()
		ids, err := search(ctx, c.Query)
		lat = append(lat, time.Since(start))
		if err != nil {
			r.Failures++
			continue
		}
		k := Rank(ids, c.Target)
		r.R1 += hit(k, 1)
		r.R5 += hit(k, 5)
		r.R10 += hit(k, 10)
		r.MRR += hit(k, 10) / float64(max(k, 1))
	}
	if n := float64(len(cases)); n > 0 {
		r.R1, r.R5, r.R10, r.MRR = r.R1/n, r.R5/n, r.R10/n, r.MRR/n
	}
	r.P50, r.P95 = percentiles(lat)
	return r, nil
}

func hit(rank, k int) float64 {
	if rank > 0 && rank <= k {
		return 1
	}
	return 0
}

// percentiles uses the nearest-rank method.
func percentiles(ds []time.Duration) (p50, p95 time.Duration) {
	if len(ds) == 0 {
		return 0, 0
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	at := func(q float64) time.Duration { return s[int(math.Ceil(q*float64(len(s))))-1] }
	return at(0.50), at(0.95)
}

// Paraphrase is one FAQ asked two other ways (eval/paraphrases.tsv).
type Paraphrase struct {
	ID                int
	Colloquial, Short string
}

// ReadParaphrases reads "id<TAB>colloquial question<TAB>short keywords" lines.
func ReadParaphrases(r io.Reader) ([]Paraphrase, error) {
	var out []Paraphrase
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		f := strings.Split(sc.Text(), "\t")
		if len(f) != 3 {
			return nil, fmt.Errorf("line %d: want 3 tab-separated fields, got %d", n, len(f))
		}
		id, err := strconv.Atoi(f[0])
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		out = append(out, Paraphrase{id, f[1], f[2]})
	}
	return out, sc.Err()
}

// WriteHeader states the data source; the CC BY 4.0 license requires attribution wherever results are shown.
func WriteHeader(w io.Writer, model string) error {
	_, err := fmt.Fprintf(w, "# data: JaGovFaqs-22k (CC BY 4.0, https://huggingface.co/datasets/matsuxr/JaGovFaqs-22k), model: %s\n", model)
	return err
}

// WriteLine prints one tab-separated result row.
func WriteLine(w io.Writer, set, method string, r Report) {
	_, _ = fmt.Fprintf(w, "%s\t%s\tn=%d\tR@1=%.2f\tR@5=%.2f\tR@10=%.2f\tMRR@10=%.3f\tp50=%dms\tp95=%dms\tfailures=%d\n",
		set, method, r.N, r.R1, r.R5, r.R10, r.MRR, r.P50.Milliseconds(), r.P95.Milliseconds(), r.Failures)
}
