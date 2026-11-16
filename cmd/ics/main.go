// Command ics serves, loads, and evaluates the helpdesk FAQ search.
package main

import (
	"fmt"
	"io"
	"os"
)

const usage = "usage: ics serve|load|eval [flags]"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, _, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "serve", "load", "eval":
		_, _ = fmt.Fprintf(stderr, "%s: not implemented\n", args[0])
		return 1
	default:
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
}
