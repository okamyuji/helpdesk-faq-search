// Package ingest reads the JaGovFaqs-22k dataset.
package ingest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"

	"github.com/okamyuji/helpdesk-faq-search/internal/textnorm"
)

// FAQ is one question and answer pair with its attribution.
type FAQ struct {
	ID       int
	Question string
	Answer   string
	Agency   string
	URL      string
	QANorm   string
}

type row struct {
	Question  string `json:"Question"`
	Answer    string `json:"Answer"`
	Copyright string `json:"copyright"`
	URL       string `json:"url"`
}

// The longest line in the dataset is about 15KB; bufio's 64KiB default leaves too little room.
const maxLine = 1 << 20

// ReadJSONL reads one FAQ per line. ID is the 0-based line number.
// Every field is required because each result must show its source.
func ReadJSONL(r io.Reader) ([]FAQ, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, maxLine)
	var out []FAQ
	for n := 0; sc.Scan(); n++ {
		var v row
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			return nil, fmt.Errorf("line %d: %w", n+1, err)
		}
		for _, f := range [...]struct{ name, val string }{{"Question", v.Question}, {"Answer", v.Answer}, {"copyright", v.Copyright}, {"url", v.URL}} {
			if f.val == "" {
				return nil, fmt.Errorf("line %d: missing %s", n+1, f.name)
			}
		}
		out = append(out, FAQ{ID: n, Question: v.Question, Answer: v.Answer, Agency: v.Copyright, URL: v.URL,
			QANorm: textnorm.NFKC(v.Question + "\n" + v.Answer)})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return out, nil
}
