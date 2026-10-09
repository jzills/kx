package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/config"
)

// A listing a --match term emptied keeps its own count label and explains
// itself in a row beneath, rather than substituting the explanation for the
// count.
//
// "Mixed · diagnostics · nothing matches 'cron'" changed the caption's shape
// depending on the result: the segment that holds a count held a sentence
// instead, so the caption could not be read at a glance across a populated
// and an empty sweep. The count belongs in the caption; the reason does not.
//
// Each caller passes its own zero label — "0 items", "0 checked", "0 images"
// — because that is the noun it already uses when it has results. kx tree
// prints no count when populated, so it passes none and the caption drops
// the segment.
func TestEmptyMatchKeepsTheCountAndExplainsInARow(t *testing.T) {
	for _, tc := range []struct {
		name        string
		kind        string
		scope       string
		zeroCount   string
		wantCaption string
	}{
		{"items", "Pods", "diagnostics", "0 items", "Pods · diagnostics · 0 items"},
		{"checked", "Mixed", "diagnostics", "0 checked", "Mixed · diagnostics · 0 checked"},
		{"images", "Mixed", "diagnostics", "0 images", "Mixed · diagnostics · 0 images"},
		// No count when populated, so none when empty either.
		{"no count", "Namespace", "diagnostics", "", "Namespace · diagnostics"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			SetOutput(&out, &bytes.Buffer{}, config.DefaultTheme)
			t.Cleanup(func() { SetOutput(nil, nil, config.DefaultTheme) })

			EmptyMatch(tc.kind, tc.scope, tc.zeroCount, "cron")

			lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
			if len(lines) != 2 {
				t.Fatalf("got %d lines, want a caption and one row:\n%s", len(lines), out.String())
			}
			if lines[0] != tc.wantCaption {
				t.Errorf("caption = %q, want %q", lines[0], tc.wantCaption)
			}
			if want := cellPad + NothingMatches("cron"); lines[1] != want {
				t.Errorf("row = %q, want %q — indented like a table row", lines[1], want)
			}
		})
	}
}
