// Package render writes kx output.
//
// Phase 1 emits plain text only. The themed styling layer (palette registry,
// semantic style names, TTY and NO_COLOR detection) replaces the bodies of
// these functions in phase 2 without changing their signatures, which is why
// commands call these rather than fmt.Print directly.
package render

import (
	"fmt"
	"io"
	"os"
	"strings"
)

var (
	out io.Writer = os.Stdout
	err io.Writer = os.Stderr
)

// SetOutput redirects rendered output, used by tests.
func SetOutput(stdout, stderr io.Writer) {
	out, err = stdout, stderr
}

// Success reports a completed action.
func Success(msg string) {
	fmt.Fprintf(out, "✓ %s\n", msg)
}

// Error reports a failed command. Errors go to stderr so piped stdout stays
// clean.
func Error(msg string) {
	fmt.Fprintf(err, "✗ %s\n", msg)
}

// Caption prints the muted "·"-joined context line above a listing, skipping
// empty parts.
func Caption(parts ...string) {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}
	fmt.Fprintln(out, strings.Join(kept, " · "))
}

// Raw prints text verbatim.
func Raw(text string) {
	fmt.Fprintln(out, text)
}
