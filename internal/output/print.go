// Package output renders command results in either human text or
// machine JSON, based on the `--json` flag.
//
// Why this lives in its own package: every subcommand has the same
// dual-output need; centralizing it avoids each cmd reaching for
// fmt.Println vs json.Marshal differently.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Printer carries the JSON-vs-text mode + a writer (defaults to stdout).
// Tests pass a *bytes.Buffer as Out to capture output.
type Printer struct {
	JSON bool
	Out  io.Writer // defaults to os.Stdout if nil
}

// New returns a Printer with the given mode writing to stdout.
func New(jsonMode bool) *Printer {
	return &Printer{JSON: jsonMode, Out: os.Stdout}
}

// writer returns the configured writer or os.Stdout when nil.
func (p *Printer) writer() io.Writer {
	if p.Out != nil {
		return p.Out
	}
	return os.Stdout
}

// Result emits either a JSON marshalling of `data` or a human-rendered
// `humanLine`. Use this for command success output. `humanLine` may be
// empty when text mode is just a confirmation noise ("done").
func (p *Printer) Result(data any, humanLine string) error {
	if p.JSON {
		enc := json.NewEncoder(p.writer())
		enc.SetIndent("", "  ")
		return enc.Encode(data)
	}
	if humanLine != "" {
		fmt.Fprintln(p.writer(), humanLine)
	}
	return nil
}

// Plain writes a one-line human message. In JSON mode it goes to stderr
// instead — diagnostic noise should not contaminate machine output.
func (p *Printer) Plain(msg string) {
	if p.JSON {
		fmt.Fprintln(os.Stderr, msg)
		return
	}
	fmt.Fprintln(p.writer(), msg)
}
