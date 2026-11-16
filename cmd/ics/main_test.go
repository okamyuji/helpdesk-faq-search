package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunUnknownSubcommandReturns2(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run([]string{"nope"}, &stdout, &stderr); got != 2 {
		t.Fatalf("exit = %d, want 2", got)
	}
	if !strings.Contains(stderr.String(), "usage: ics serve|load|eval") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunNoArgsReturns2(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run(nil, &stdout, &stderr); got != 2 {
		t.Fatalf("exit = %d, want 2", got)
	}
	if !strings.Contains(stderr.String(), "usage: ics serve|load|eval") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
