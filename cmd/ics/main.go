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
	case "serve", "load":
		required := map[string][]string{"serve": serveRequired, "load": loadRequired}[args[0]]
		cfg, err := loadConfig(getenv, required)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 2
		}
		if args[0] == "serve" {
			err = serve(cfg)
		} else {
			err = loadAll(cfg, stdout)
		}
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "eval":
		_, _ = fmt.Fprintf(stderr, "%s: not implemented\n", args[0])
		return 1
	default:
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
}
