package main

import (
	"bytes"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func full() map[string]string {
	return map[string]string{"DATABASE_URL": "postgres://db/x", "TEI_EMBED_URL": "http://localhost:8081",
		"RERANK_MODEL_ARN": "arn:aws:bedrock:ap-northeast-1::foundation-model/cohere.rerank-v3-5:0"}
}

func TestLoadConfigDefaults(t *testing.T) {
	c, err := loadConfig(env(full()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Model != "ruri-v3-30m" || c.Addr != ":8080" || c.Region != "ap-northeast-1" || c.HelpdeskURL == "" {
		t.Fatalf("config = %+v", c)
	}
}

func TestLoadConfigReadsOverrides(t *testing.T) {
	m := full()
	m["EMBEDDING_MODEL"], m["LISTEN_ADDR"], m["AWS_REGION"], m["HELPDESK_URL"] = "ruri-v3-310m", ":9000", "us-west-2", "https://help.example.com"
	c, err := loadConfig(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.Model != "ruri-v3-310m" || c.Addr != ":9000" || c.Region != "us-west-2" || c.HelpdeskURL != "https://help.example.com" ||
		c.DatabaseURL != "postgres://db/x" || c.TEIURL != "http://localhost:8081" || !strings.HasSuffix(c.RerankARN, "rerank-v3-5:0") {
		t.Fatalf("config = %+v", c)
	}
}

func TestLoadConfigRejectsMissingRequired(t *testing.T) {
	for _, k := range []string{"DATABASE_URL", "TEI_EMBED_URL", "RERANK_MODEL_ARN"} {
		m := full()
		delete(m, k)
		if _, err := loadConfig(env(m)); err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("without %s: err = %v", k, err)
		}
	}
}

func TestLoadConfigRejectsUnknownModel(t *testing.T) {
	m := full()
	m["EMBEDDING_MODEL"] = "ruri-v3-70m"
	if _, err := loadConfig(env(m)); err == nil {
		t.Fatal("want an error for an unknown model")
	}
}

func TestRunServeWithoutConfigFails(t *testing.T) {
	var stderr bytes.Buffer
	if got := run([]string{"serve"}, env(map[string]string{}), &bytes.Buffer{}, &stderr); got != 2 || !strings.Contains(stderr.String(), "DATABASE_URL") {
		t.Fatalf("exit = %d, stderr = %q", got, stderr.String())
	}
}
