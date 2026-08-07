package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/jzills/kx/internal/theme"
	"github.com/muesli/termenv"
)

func styledCapture(t *testing.T, themeName string, render func(*Renderer)) string {
	t.Helper()
	var buf bytes.Buffer
	renderer := newWithProfile(&buf, &buf, themeName, termenv.TrueColor)
	render(renderer)
	return buf.String()
}

// capture renders into a buffer. A buffer is not a terminal, so styling is off
// and what comes back is pure layout.
func capture(render func(*Renderer)) string {
	var buf bytes.Buffer
	renderer := New(&buf, &buf, "github-dark", false)
	render(renderer)
	return buf.String()
}

const esc = "\x1b["

// Styling reaches a terminal that wants it.
func TestStyledOutputEmitsColor(t *testing.T) {
	out := styledCapture(t, "github-dark", func(r *Renderer) { r.Caption("Pods", "prod") })
	if !strings.Contains(out, esc) {
		t.Errorf("styled output carries no escape sequences: %q", out)
	}
	if !strings.Contains(out, "Pods · prod") {
		t.Errorf("styled output lost its text: %q", out)
	}
}

// Piped or redirected output must stay clean for grep and awk: a buffer is not
// a terminal, so nothing is styled.
func TestNonTerminalOutputIsPlain(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, &buf, "github-dark", false).Caption("Pods", "prod")
	if strings.Contains(buf.String(), esc) {
		t.Errorf("non-terminal output was styled: %q", buf.String())
	}
}

func TestPlainDisablesStyling(t *testing.T) {
	var buf bytes.Buffer
	newWithProfile(&buf, &buf, "github-dark", termenv.Ascii).Caption("Pods", "prod")
	if strings.Contains(buf.String(), esc) {
		t.Errorf("plain output was styled: %q", buf.String())
	}
}

// https://no-color.org/ — honored even on a terminal that supports color.
func TestNoColorEnvDisablesStyling(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer
	New(&buf, &buf, "github-dark", false).Caption("Pods", "prod")
	if strings.Contains(buf.String(), esc) {
		t.Errorf("NO_COLOR output was styled: %q", buf.String())
	}
}

// Errors go to stderr so a failed command doesn't pollute a pipeline reading
// stdout.
func TestErrorWritesToStderr(t *testing.T) {
	var out, errOut bytes.Buffer
	New(&out, &errOut, "github-dark", false).Error("boom")
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
	if !strings.Contains(errOut.String(), "boom") {
		t.Errorf("stderr = %q, want the message", errOut.String())
	}
	if !strings.HasPrefix(errOut.String(), "✗") {
		t.Errorf("stderr = %q, want the ✗ marker", errOut.String())
	}
}

func TestSuccessMarker(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, &buf, "github-dark", false).Success("done")
	if got := buf.String(); got != "✓ done\n" {
		t.Errorf("Success = %q, want %q", got, "✓ done\n")
	}
}

// Quoted fragments are accented so the resource a command acted on stands out.
func TestQuotedFragmentsAreAccented(t *testing.T) {
	plain := styledCapture(t, "github-dark", func(r *Renderer) { r.Success("Deleted 'nginx-abc'") })
	// Three styled runs: marker, the text around the quotes, and the quoted part.
	if strings.Count(plain, esc) < 3 {
		t.Errorf("quoted fragment not styled separately: %q", plain)
	}
	if !strings.Contains(plain, "'nginx-abc'") {
		t.Errorf("quotes were dropped: %q", plain)
	}
}

func TestCaptionSkipsEmptyParts(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, &buf, "github-dark", false).Caption("Themes", "", "11 items")
	if got := buf.String(); got != "Themes · 11 items\n" {
		t.Errorf("Caption = %q, want %q", got, "Themes · 11 items\n")
	}
}

func TestStatusColor(t *testing.T) {
	cases := map[string]string{
		"Running":           theme.StatusOK,
		"Completed":         theme.StatusOK,
		"CrashLoopBackOff":  theme.StatusBad,
		"OOMKilled":         theme.StatusBad,
		"Pending":           theme.StatusWarn,
		"Terminating":       theme.StatusWarn,
		"ContainerCreating": theme.StatusWarn,
		"Init:0/2":          theme.StatusWarn,
		"SomethingUnknown":  theme.StatusNeutral,
	}
	for status, want := range cases {
		if got := statusColor(status); got != want {
			t.Errorf("statusColor(%q) = %q, want %q", status, got, want)
		}
	}
}

// Thresholds mirror kx diag: a red 94% means what a critical finding means.
func TestUsagePctColor(t *testing.T) {
	cases := []struct {
		cell, resource, want string
	}{
		{"94%", "memory", theme.StatusBad},
		{"90%", "memory", theme.StatusBad},
		{"80%", "memory", theme.StatusWarn},
		{"75%", "memory", theme.StatusWarn},
		{"74%", "memory", ""},
		{"95%", "cpu", theme.StatusWarn},
		{"90%", "cpu", theme.StatusWarn},
		{"89%", "cpu", ""},
		{"n/a", "cpu", ""},
		{"12", "cpu", ""},
	}
	for _, tc := range cases {
		if got := usagePctColor(tc.cell, tc.resource); got != tc.want {
			t.Errorf("usagePctColor(%q, %q) = %q, want %q", tc.cell, tc.resource, got, tc.want)
		}
	}
}

func TestStatusStyleClassifies(t *testing.T) {
	cases := map[string]string{
		"Running":           theme.StatusOK,
		"CrashLoopBackOff":  theme.StatusBad,
		"Pending":           theme.StatusWarn,
		"ContainerCreating": theme.StatusWarn,
		"Init:0/2":          theme.StatusWarn,
		"WhoKnows":          theme.StatusNeutral,
	}
	for status, want := range cases {
		if got := StatusStyle(status); got != want {
			t.Errorf("StatusStyle(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestUsageStyleThresholds(t *testing.T) {
	cases := []struct {
		pct      int
		resource string
		want     string
	}{
		{74, "memory", ""},
		{75, "memory", theme.StatusWarn},
		{89, "memory", theme.StatusWarn},
		{90, "memory", theme.StatusBad},
		{89, "cpu", ""},
		{90, "cpu", theme.StatusWarn},
		// CPU never reaches critical: throttling degrades, it doesn't crash.
		{100, "cpu", theme.StatusWarn},
	}
	for _, c := range cases {
		if got := UsageStyle(c.pct, c.resource); got != c.want {
			t.Errorf("UsageStyle(%d, %q) = %q, want %q", c.pct, c.resource, got, c.want)
		}
	}
}

// A cell like "17 (3h ago)" must still align its count, which right-aligning
// the whole cell would not do.
func TestAlignRestartsPadsNumericPrefixOnly(t *testing.T) {
	rows := [][]string{{"a", "0"}, {"b", "17"}, {"c", "3 (2h ago)"}}
	alignRestarts(rows, 1)
	want := []string{" 0", "17", " 3 (2h ago)"}
	for i, row := range rows {
		if row[1] != want[i] {
			t.Errorf("rows[%d][1] = %q, want %q", i, row[1], want[i])
		}
	}
}

func TestAlignRestartsIgnoresNonNumeric(t *testing.T) {
	rows := [][]string{{"a", "none"}, {"b", "also"}}
	alignRestarts(rows, 1)
	if rows[0][1] != "none" || rows[1][1] != "also" {
		t.Errorf("non-numeric cells were padded: %v", rows)
	}
}

// Each row previews its own palette, so the list shows what you'd switch to.
func TestThemeListPreviewsEachPaletteDistinctly(t *testing.T) {
	out := styledCapture(t, "github-dark", func(r *Renderer) { r.ThemeList("dracula") })
	for _, name := range theme.Names() {
		if !strings.Contains(out, name) {
			t.Errorf("theme list is missing %q", name)
		}
	}
	if !strings.Contains(out, "→") {
		t.Errorf("active theme is not marked: %q", out)
	}
	// github-dark's success color and dracula's must both appear, which they
	// only do if each row is styled with its own palette.
	for _, rgb := range []string{"63;185;80", "80;250;123"} {
		if !strings.Contains(out, rgb) {
			t.Errorf("theme list did not preview palette %s:\n%s", rgb, out)
		}
	}
}

// Regression for the actual reported bug: after `kx theme light`, the
// "light" row's index/name went blank on a dark terminal. An earlier fix
// made every row preview in its own palette, which fixed *other* rows
// leaking the active theme's color but left this exact case broken: the
// active row is "light" previewing itself, so it used light's own Body
// (near-black) regardless. The index/marker/name columns must never carry a
// palette color at all — only bold, for the active row — so this can't
// recur under any active/previewed theme combination.
func TestThemeListIndexAndNameColumnsNeverCarryPaletteColor(t *testing.T) {
	out := styledCapture(t, "github-dark", func(r *Renderer) { r.ThemeList("light") })
	for _, want := range []string{esc + "1m10" + esc + "0m", esc + "1m→" + esc + "0m", esc + "1mlight" + esc + "0m"} {
		if !strings.Contains(out, want) {
			t.Errorf("active row's index/marker/name = %q not found bold-and-uncolored in:\n%s", want, out)
		}
	}
	// Bold combines with a color in one escape ("\x1b[1;38;2;..."), so a
	// literal "\x1b[1;" immediately preceding these cells' text would mean a
	// color crept back in.
	for _, bad := range []string{esc + "1;38;2;36;40;47m10", esc + "1;38;2;36;40;47mlight"} {
		if strings.Contains(out, bad) {
			t.Errorf("active row's index/name carried a palette color:\n%s", out)
		}
	}
}

// The non-active rows' index/name must also stay uncolored, regardless of
// which theme previews there or which theme is active — not just the active
// row from the test above.
func TestThemeListNonActiveRowsIndexAndNameAreUnstyled(t *testing.T) {
	out := styledCapture(t, "light", func(r *Renderer) { r.ThemeList("light") })
	if !strings.Contains(out, "dracula") {
		t.Fatalf("theme list is missing dracula:\n%s", out)
	}
	// dracula's own Muted (#6272a4 -> lipgloss's 97;113;163, confirmed by a
	// throwaway render rather than hand-converted hex; colorful's sRGB
	// round-trip can shift a channel by 1) must not precede its plain,
	// unstyled name text.
	if strings.Contains(out, esc+"38;2;97;113;163mdracula") {
		t.Errorf("non-active row's name carried a palette color:\n%s", out)
	}
}

// A preview must not emit color into a pipe just because it builds its own
// palette.
func TestThemeListPlainWhenNotStyled(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, &buf, "github-dark", false).ThemeList("github-dark")
	if strings.Contains(buf.String(), esc) {
		t.Errorf("theme previews leaked color into unstyled output: %q", buf.String())
	}
}

// A cell can arrive already styled — the theme previews render in their own
// palette — and the ANSI bytes must not count toward the column width. Getting
// this wrong pads the column to several times its real width, which the plain
// -mode layout goldens cannot catch because they contain no escape sequences.
func TestStyledCellsDoNotInflateColumnWidth(t *testing.T) {
	out := styledCapture(t, "github-dark", func(r *Renderer) { r.ThemeList("github-dark") })

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// Skip the caption, which is not part of the table.
	widths := map[int]bool{}
	for _, line := range lines[1:] {
		widths[lipgloss.Width(line)] = true
	}
	if len(widths) != 1 {
		t.Errorf("table rows have %d different display widths, want 1: %v", len(widths), widths)
	}
	for w := range widths {
		// The longest theme name is 16 chars and the swatch about 40; anything
		// near 150 means escape bytes were counted as visible.
		if w > 100 {
			t.Errorf("row display width = %d, far wider than the visible content", w)
		}
	}
}

func TestFormatAge(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		ago  time.Duration
		want string
	}{
		{3 * time.Second, "3s ago"},
		{90 * time.Second, "1m ago"},
		{2 * time.Hour, "2h ago"},
		{50 * time.Hour, "2d ago"},
		{0, "0s ago"},
	}
	for _, tc := range cases {
		if got := formatAgeAt(now, now.Add(-tc.ago)); got != tc.want {
			t.Errorf("formatAgeAt(-%v) = %q, want %q", tc.ago, got, tc.want)
		}
	}
}

// An unset timestamp renders as nothing rather than as a wrong age.
func TestFormatAgeZeroTime(t *testing.T) {
	if got := FormatAge(time.Time{}); got != "" {
		t.Errorf("FormatAge(zero) = %q, want empty", got)
	}
}

// Clock skew between the API server and here would otherwise render "in 3m".
func TestFormatAgeFutureIsJustNow(t *testing.T) {
	now := time.Now()
	if got := formatAgeAt(now, now.Add(time.Minute)); got != "just now" {
		t.Errorf("future timestamp = %q, want \"just now\"", got)
	}
}

func TestEllipsize(t *testing.T) {
	cases := []struct {
		text string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exactly-10", 10, "exactly-10"},
		{"this is far too long", 10, "this is f…"},
		{"anything", 0, "anything"},
	}
	for _, tc := range cases {
		if got := ellipsize(tc.text, tc.n); got != tc.want {
			t.Errorf("ellipsize(%q, %d) = %q, want %q", tc.text, tc.n, got, tc.want)
		}
	}
}

// A flexed column gives back what the rest of the table overruns, so a long
// value is cut rather than wrapping every row across two lines.
func TestFlexColumnShrinksToFit(t *testing.T) {
	var buf bytes.Buffer
	renderer := New(&buf, &buf, "github-dark", false)

	columns := []Column{{Header: "NAME"}, {Header: "DETAIL", Flex: true}}
	rows := [][]Cell{{Plain("web"), Plain(strings.Repeat("x", 400))}}
	renderer.Table(columns, rows)

	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		// Off-terminal the renderer targets the wide pipe width, so nothing
		// should be cut here.
		if !strings.Contains(line, strings.Repeat("x", 400)) && strings.Contains(line, "x") {
			t.Errorf("piped output was truncated: %q", line)
		}
	}
}

// Table renders rows it does not own. Ellipsizing the flexed column in place
// edits the caller's slice, so rendering the same rows again cuts an
// already-cut value — the second table comes out narrower than the first for no
// reason the caller can see.
func TestFittingTheFlexColumnLeavesTheCallerRowsAlone(t *testing.T) {
	long := strings.Repeat("x", 200)
	columns := []Column{{Header: "NAME"}, {Header: "DETAIL", Flex: true}}
	rows := [][]Cell{{Plain("web"), Plain(long)}}

	fitted := fitFlexColumn(columns, rows, []int{3, 200}, 40)

	if rows[0][1].Text != long {
		t.Errorf("caller's row was cut in place: %q", rows[0][1].Text)
	}
	if fitted[0][1].Text == long {
		t.Error("the rendered row was not shortened to fit")
	}
	if !strings.HasSuffix(fitted[0][1].Text, "…") {
		t.Errorf("shortened cell = %q, want an ellipsis marking the cut", fitted[0][1].Text)
	}
}

// Without a flex column the table keeps its natural width, which is what every
// other listing relies on.
func TestTableWithoutFlexIsUnconstrained(t *testing.T) {
	var buf bytes.Buffer
	renderer := New(&buf, &buf, "github-dark", false)
	renderer.Table([]Column{{Header: "A"}}, [][]Cell{{Plain(strings.Repeat("y", 300))}})
	if !strings.Contains(buf.String(), strings.Repeat("y", 300)) {
		t.Error("a table with no flex column was truncated")
	}
}
