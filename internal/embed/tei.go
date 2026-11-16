// Package embed computes Ruri v3 embeddings through text-embeddings-inference (TEI).
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Ruri v3 expects these prefixes; see ADR 0010.
const (
	PrefixQuery = "検索クエリ: "
	PrefixDoc   = "検索文書: "
	PrefixPlain = ""
)

const defaultBatch = 32

// A stuck TEI would otherwise block a load task forever; one batch of 32 takes seconds.
var defaultHTTP = &http.Client{Timeout: 2 * time.Minute}

// Client calls TEI's /embed. A zero HTTP or Batch uses a client with a 2-minute timeout and 32.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Batch   int
}

// Embed returns one normalized vector per text, in the same order.
func (c *Client) Embed(ctx context.Context, prefix string, texts []string) ([][]float32, error) {
	size := c.Batch
	if size <= 0 {
		size = defaultBatch
	}
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += size {
		end := min(start+size, len(texts))
		vecs, err := c.post(ctx, prefix, texts[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (c *Client) post(ctx context.Context, prefix string, texts []string) ([][]float32, error) {
	inputs := make([]string, len(texts))
	for i, t := range texts {
		inputs[i] = prefix + t
	}
	body, err := json.Marshal(map[string]any{"inputs": inputs, "normalize": true, "truncate": true})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = defaultHTTP
	}
	res, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tei embed: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return nil, fmt.Errorf("tei embed: status %d: %s", res.StatusCode, bytes.TrimSpace(msg))
	}
	var vecs [][]float32
	if err := json.NewDecoder(res.Body).Decode(&vecs); err != nil {
		return nil, fmt.Errorf("tei embed: decode: %w", err)
	}
	if len(vecs) != len(texts) {
		return nil, fmt.Errorf("tei embed: %d vectors for %d inputs", len(vecs), len(texts))
	}
	return vecs, nil
}
