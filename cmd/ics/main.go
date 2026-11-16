// Command ics serves, loads, and evaluates the helpdesk FAQ search.
package main

import (
	"fmt"
	"io"
	"os"
)

const usage = "usage: ics serve|load|eval [flags]"

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "serve", "load", "eval":
		required := map[string][]string{"serve": serveRequired, "load": loadRequired, "eval": serveRequired}[args[0]]
		cfg, err := loadConfig(getenv, required)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 2
		}
		switch args[0] {
		case "serve":
			err = serve(cfg)
		case "load":
			err = loadAll(cfg, stdout)
		default:
			err = evalAll(cfg, stdout)
		}
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	default:
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
}
