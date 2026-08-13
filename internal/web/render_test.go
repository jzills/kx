package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"html"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/scanner"
	"github.com/jzills/kx/internal/theme"
	"github.com/jzills/kx/internal/tree"
)

func testMeta(t *testing.T) Meta {
	t.Helper()
	styles, err := theme.WebStyles(theme.Default)
	if err != nil {
		t.Fatalf("WebStyles returned %v", err)
	}
	return Meta{
		// The title starts at the command, the way pageMeta's callers spell
		// it; the invocation is the command line itself, so it keeps its "kx".
		Title:      "diag · diagnostics",
		Invocation: "kx diag 1",
		Captured:   time.Date(2026, 8, 1, 9, 41, 22, 0, time.UTC),
		URL:        "http://127.0.0.1:41287",
		Styles:     styles,
	}
}

func quantity(t *testing.T, value string) *resource.Quantity {
	t.Helper()
	q, err := resource.ParseQuantity(value)
	if err != nil {
		t.Fatalf("ParseQuantity(%q) returned %v", value, err)
	}
	return &q
}

// jsonEscapedLT/jsonEscapedGT are json.Marshal's default HTML-escaped forms
// of "<" and ">": the six-character literal escape sequence
// backslash-u-0-0-3-c (and 3e), not the "&lt;"/"&gt;" entities the old
// html/template-based rendering used. Built from rune(0x5c) rather than
// typed as a literal backslash in a string, so there is no ambiguity about
// whether the source holds that six-character escape text or an actual
// "<"/">" rune — the two look identical in some renderings of this source
// but must not be confused, since the whole point of the test using them is
// to tell escaped output apart from unescaped output.
var (
	jsonEscapedLT = string(rune(0x5c)) + "u003c"
	jsonEscapedGT = string(rune(0x5c)) + "u003e"
)

// extractJSONScript returns the raw text content of the
// <script type="application/json" id="ID">...</script> block a grid
// initializes from, so tests can decode the data a page hands to Tabulator
// rather than pattern-matching on markup a client-side library now owns.
func extractJSONScript(t *testing.T, html, id string) string {
	t.Helper()
	marker := `id="` + id + `"`
	at := strings.Index(html, marker)
	if at < 0 {
		t.Fatalf("no <script> with id=%q found in output", id)
	}
	openTag := strings.Index(html[at:], ">")
	if openTag < 0 {
		t.Fatalf("script tag for id=%q was never closed", id)
	}
	start := at + openTag + 1
	relEnd := strings.Index(html[start:], "</script>")
	if relEnd < 0 {
		t.Fatalf("no closing </script> found for id=%q", id)
	}
	return html[start : start+relEnd]
}

func decodeDiagRows(t *testing.T, html string) []DiagRow {
	t.Helper()
	var rows []DiagRow
	raw := extractJSONScript(t, html, "kx-diag-data")
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatalf("could not decode diag JSON payload: %v\nraw: %s", err, raw)
	}
	return rows
}

func decodeScanImageRows(t *testing.T, html string) []ScanImageRow {
	t.Helper()
	var rows []ScanImageRow
	raw := extractJSONScript(t, html, "kx-scan-images-data")
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatalf("could not decode scan image JSON payload: %v\nraw: %s", err, raw)
	}
	return rows
}

func decodeScanFindingRows(t *testing.T, html string) []ScanFindingRow {
	t.Helper()
	var rows []ScanFindingRow
	raw := extractJSONScript(t, html, "kx-scan-findings-data")
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatalf("could not decode scan findings JSON payload: %v\nraw: %s", err, raw)
	}
	return rows
}

func decodeTreeRows(t *testing.T, html string) []TreeRow {
	t.Helper()
	var rows []TreeRow
	raw := extractJSONScript(t, html, "kx-tree-data")
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatalf("could not decode tree JSON payload: %v\nraw: %s", err, raw)
	}
	return rows
}

func criticalReport(t *testing.T) diagnostics.Report {
	t.Helper()
	return diagnostics.Report{
		Kind: kinds.Deployment, Name: "api-gateway", Namespace: "diagnostics",
		Verdict: diagnostics.Critical,
		Findings: []diagnostics.Finding{
			{Severity: diagnostics.Critical, Summary: "2 of 3 replicas unavailable"},
			{Severity: diagnostics.Warning, Summary: "Container 'gateway' memory at 91% of limit"},
		},
		Pods: []diagnostics.PodDiagnostic{{
			Name: "api-gateway-7d4f9c-2xk8p", Phase: "Running",
			ReadyContainers: 1, TotalContainers: 2,
			Containers: []diagnostics.ContainerDiagnostic{{
				Name: "gateway", RestartCount: 17, State: "Waiting",
				WaitingReason: "CrashLoopBackOff",
				LogLines:      []string{"FATAL listen tcp :8080: bind: address already in use"},
				LogFiltered:   true,
				MemoryUsage:   quantity(t, "466Mi"), MemoryLimit: quantity(t, "512Mi"),
			}},
		}},
		WarningEvents: []diagnostics.EventSummary{{
			Reason: "BackOff", Message: "Back-off restarting failed container",
			Kind: "Pod", Name: "api-gateway-7d4f9c-2xk8p", Count: 34,
			// Fixed, 4 minutes before testMeta's Captured (09:41:22 UTC) rather
			// than time.Now(): the fixture must not be clock-dependent, or two
			// renders of "the same" report would render different ages.
			LastTimestamp: time.Date(2026, 8, 1, 9, 37, 22, 0, time.UTC),
		}},
	}
}

func TestRenderDiagSingleResource(t *testing.T) {
	page := DiagPage{
		Meta: testMeta(t), Scope: "diagnostics", Single: true,
		Reports: []diagnostics.Report{criticalReport(t)},
	}
	out, err := RenderDiag(page)
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	html := string(out)

	for _, want := range []string{
		"<!doctype html>",
		"Deployment/api-gateway",
		"2 of 3 replicas unavailable",
		"CrashLoopBackOff",
		"status-bad",
		// 466Mi of 512Mi is 91%, over the memory-critical threshold.
		`class="meter-fill status-bad" style="width:91%"`,
		"--status-bad:#f85149;",
		"BackOff",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered page is missing %q", want)
		}
	}
}

// diag's single-resource view has exactly one resource — nothing to sort,
// filter, or group. A regression that started emitting a grid mount here
// would be silently pointless (an empty control surface over one row)
// rather than caught, without this.
func TestRenderDiagSingleHasNoGridMount(t *testing.T) {
	out, err := RenderDiag(DiagPage{
		Meta: testMeta(t), Single: true,
		Reports: []diagnostics.Report{criticalReport(t)},
	})
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	html := string(out)
	// Checked via the id="..." attribute form and the full div opening tag,
	// not bare "kx-diag-data"/"data-kx-grid=\"diag\"" substrings:
	// kx-grid.js's own source (embedded on every page regardless of type)
	// references those same strings as JS arguments/selectors
	// (kxData("kx-diag-data"), querySelector('[data-kx-grid="diag"]')), so a
	// bare substring check would pass unconditionally whether or not this
	// page actually emitted the mount div or script tag.
	if strings.Contains(html, `class="grid-mount" data-kx-grid="diag"`) || strings.Contains(html, `id="kx-diag-data"`) {
		t.Error("a single-resource page emitted a diag grid mount/JSON payload")
	}
}

// A container with no limit set must render an em dash. Rendering 0% would
// claim it is using none of a limit it does not have.
func TestRenderDiagShowsDashWhenNoLimit(t *testing.T) {
	report := criticalReport(t)
	report.Pods[0].Containers[0].MemoryLimit = nil
	report.Pods[0].Containers[0].MemoryUsage = quantity(t, "466Mi")

	out, err := RenderDiag(DiagPage{
		Meta: testMeta(t), Single: true,
		Reports: []diagnostics.Report{report},
	})
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	// A bare "0%" would also match the embedded stylesheet's own "width:
	// 100%"/"height: 100%" rules, so this checks the specific text a wrongly
	// computed usage would render instead.
	if strings.Contains(string(out), `>0%</span>`) {
		t.Error("a container with no limit rendered a percentage")
	}
	if !strings.Contains(string(out), `<td class="dim">—</td>`) {
		t.Error("a container with no limit did not render an em dash")
	}
}

// usageOf must agree with kx diag's finding text (usagePercent in
// internal/diagnostics/findings.go) and kx top's MEM%/CPU% columns
// (percentCell in internal/cli/top.go), both of which compute this as a
// scaled-integer ratio. A float64 division used to do the same arithmetic in
// floating point and round down at ratios where the two disagree: 29Mi of
// 50Mi is the smallest such case (documented in the fix-wave review) —
// exact integer math gives 58%, but
// float64(29000)/float64(50000)*100 evaluates to 57.99999999999999, which
// int() truncates to 57.
func TestUsageOfMatchesIntegerArithmetic(t *testing.T) {
	got := usageOf(quantity(t, "29Mi"), quantity(t, "50Mi"), "memory")
	if !got.Known {
		t.Fatal("usageOf reported the limit as unknown")
	}
	if got.Pct != 58 {
		t.Errorf("Pct = %d, want 58 — the page must not disagree with kx diag "+
			"and kx top over one point of rounding", got.Pct)
	}
}

// Log lines and event messages are cluster-controlled. Anything a pod can
// print into its own logs must not become markup. The single-resource view
// still renders the report as literal server-side HTML (it has no grid), so
// this stays an html/template escaping check.
func TestRenderDiagEscapesClusterContent(t *testing.T) {
	report := criticalReport(t)
	report.Pods[0].Containers[0].LogLines = []string{`<script>alert("xss")</script>`}
	report.WarningEvents[0].Message = `he said "boom" & <b>left</b>`
	report.Name = `<img src=x onerror=alert(1)>`

	out, err := RenderDiag(DiagPage{
		Meta: testMeta(t), Single: true,
		Reports: []diagnostics.Report{report},
	})
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	html := string(out)

	for _, forbidden := range []string{
		"<script>alert",
		"<img src=x",
		"<b>left</b>",
	} {
		if strings.Contains(html, forbidden) {
			t.Errorf("unescaped cluster content in output: %q", forbidden)
		}
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Error("script tag was not escaped")
	}
}

// A healthy resource still renders a page, with the empty states filled in.
func TestRenderDiagHealthyResource(t *testing.T) {
	out, err := RenderDiag(DiagPage{
		Meta: testMeta(t), Single: true,
		Reports: []diagnostics.Report{{
			Kind: kinds.Pod, Name: "quiet", Namespace: "diagnostics",
			Verdict: diagnostics.OK,
		}},
	})
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	html := string(out)
	if !strings.Contains(html, "No issues detected") {
		t.Error("healthy report did not render its empty summary")
	}
	if !strings.Contains(html, "No warning events") {
		t.Error("healthy report did not render its empty events state")
	}
}

// The palette reaches the page as custom properties, so kx theme changes the
// browser output.
func TestRenderDiagUsesTheActivePalette(t *testing.T) {
	styles, err := theme.WebStyles("dracula")
	if err != nil {
		t.Fatalf("WebStyles returned %v", err)
	}
	meta := testMeta(t)
	meta.Styles = styles

	out, err := RenderDiag(DiagPage{
		Meta: meta, Single: true,
		Reports: []diagnostics.Report{criticalReport(t)},
	})
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	if !strings.Contains(string(out), "--background:#282a36;") {
		t.Error("dracula's background did not reach the page")
	}
}

// Ages must be measured from the page's capture time, not the clock: moving
// Captured forward has to move the rendered age with it. If age were bound to
// time.Now() these two renders would be identical, which is the regression
// this guards. (An earlier version of this test rendered the same page twice
// back-to-back, which formatAgeAt's whole-minute bucketing would satisfy even
// with age still wired to the real clock — it could not fail on the
// regression it named.)
func TestRenderDiagAgesFollowTheCaptureTime(t *testing.T) {
	report := criticalReport(t)
	event := report.WarningEvents[0].LastTimestamp

	atFourMinutes := DiagPage{
		Meta: testMeta(t), Single: true,
		Reports: []diagnostics.Report{report},
	}
	atFourMinutes.Captured = event.Add(4 * time.Minute)

	atTwoHours := DiagPage{
		Meta: testMeta(t), Single: true,
		Reports: []diagnostics.Report{report},
	}
	atTwoHours.Captured = event.Add(2 * time.Hour)

	early, err := RenderDiag(atFourMinutes)
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	late, err := RenderDiag(atTwoHours)
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}

	if bytes.Equal(early, late) {
		t.Fatal("moving Captured did not change the rendered age — age is not bound to the capture time")
	}
	if want := render.FormatAgeAt(atFourMinutes.Captured, event); !strings.Contains(string(early), want) {
		t.Errorf("page captured 4m after the event does not contain %q", want)
	}
	if want := render.FormatAgeAt(atTwoHours.Captured, event); !strings.Contains(string(late), want) {
		t.Errorf("page captured 2h after the event does not contain %q", want)
	}

	// The weaker property, kept alongside the real one above: the same page
	// value rendered twice must still be byte-equal.
	again, err := RenderDiag(atFourMinutes)
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	if !bytes.Equal(early, again) {
		t.Error("two renders of the same page produced different bytes")
	}
}

func sweepPage(t *testing.T) DiagPage {
	t.Helper()
	warning := diagnostics.Report{
		Kind: kinds.PersistentVolumeClaim, Name: "storage-pending",
		Namespace: "diagnostics", Verdict: diagnostics.Warning,
		Findings: []diagnostics.Finding{
			{Severity: diagnostics.Warning, Summary: "Volume is Pending"},
		},
	}
	return DiagPage{
		Meta: testMeta(t), Scope: "diagnostics",
		Checked: 14,
		Reports: []diagnostics.Report{criticalReport(t), warning},
	}
}

func TestRenderDiagSweepIndexesRows(t *testing.T) {
	out, err := RenderDiag(sweepPage(t))
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	html := string(out)

	for _, want := range []string{
		"14 checked",
		"kx diag &lt;index&gt; for the same detail in your terminal",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("sweep page is missing %q", want)
		}
	}
	// The full div opening tag, not a bare data-kx-grid="diag" substring:
	// kx-grid.js's own source (embedded on every page) contains that same
	// text inside its querySelector call, so a bare substring check would
	// pass even if this page never emitted the mount div itself.
	if !strings.Contains(html, `class="grid-mount" data-kx-grid="diag"`) {
		t.Error("sweep page is missing its diag grid mount")
	}

	rows := decodeDiagRows(t, html)
	if len(rows) != 2 {
		t.Fatalf("got %d diag rows, want 2", len(rows))
	}
	if rows[0].Index != 1 || rows[1].Index != 2 {
		t.Errorf("rows are not 1-based indexed: got Index %d, %d", rows[0].Index, rows[1].Index)
	}
	if rows[1].Name != "storage-pending" || rows[1].TopFinding != "Volume is Pending" {
		t.Errorf("row 1 does not carry the warning report's name/finding: %+v", rows[1])
	}

	// The drill-down is the same report block, cloned client-side from a
	// <template> keyed by the row's 0-based position (see diagRowFormatter
	// in kx-grid.js), so the detail is present in the document even though
	// it is not part of the visible grid markup.
	if !strings.Contains(html, `<template id="diag-detail-0">`) || !strings.Contains(html, "CrashLoopBackOff") {
		t.Error("sweep rows are not paired with a detail <template>")
	}
}

// A cluster-wide sweep saves no state, so kx-grid.js's column config drops
// the index column and adds namespace instead (diagColumns in kx-grid.js) —
// a client-side decision this test can't observe directly. What it pins is
// the one thing the server controls: the mount's data-all-namespaces
// attribute, which is what that column choice branches on. The grid adds a
// Namespace column on that flag; it keeps the index column either way, since
// an -A sweep is indexed now.
func TestRenderDiagAllNamespacesFlagsItsGridMount(t *testing.T) {
	page := sweepPage(t)
	page.AllNamespaces = true
	page.Scope = "all namespaces"

	out, err := RenderDiag(page)
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	html := string(out)

	if !strings.Contains(html, `data-all-namespaces="true"`) {
		t.Error("an all-namespaces sweep did not flag its grid mount")
	}
	if !strings.Contains(html, "all namespaces") {
		t.Error("scope caption missing")
	}

	indexed, err := RenderDiag(sweepPage(t))
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	if strings.Contains(string(indexed), `data-all-namespaces="true"`) {
		t.Error("an indexed sweep wrongly flagged its grid mount as all-namespaces")
	}
}

// A sweep with only healthy resources still shows them as rows in the grid,
// unlike the terminal's short "N checked · all healthy" line: the HTML report
// is the full inventory, and its grid — not the server — is where healthy
// rows get filtered away, if a viewer wants that.
func TestRenderDiagSweepAllHealthyStillShowsRows(t *testing.T) {
	page := sweepPage(t)
	page.Checked = 1
	page.Reports = []diagnostics.Report{{
		Kind: kinds.Pod, Name: "quiet", Namespace: "diagnostics", Verdict: diagnostics.OK,
	}}

	out, err := RenderDiag(page)
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	html := string(out)
	if strings.Contains(html, "all healthy") {
		t.Error("an all-healthy sweep collapsed to the one-line summary instead of rendering its grid")
	}
	if !strings.Contains(html, `class="grid-mount" data-kx-grid="diag"`) {
		t.Error("an all-healthy sweep did not render its grid")
	}
	rows := decodeDiagRows(t, html)
	if len(rows) != 1 || rows[0].Verdict != "healthy" {
		t.Errorf("rows = %+v, want the one healthy resource shown", rows)
	}
}

// Kind, Namespace, Name and finding summaries are all cluster-controlled,
// and now reach the page only through the JSON payload — the sweep's
// visible rows are drawn client-side by kx-grid.js, not server-rendered
// HTML text. json.Marshal's default HTML-escaping (< > & become <
// > &) is what stops any of them from breaking out of the
// <script type="application/json"> block; this pins that escaping, and that
// the values still round-trip to their original form once decoded — a
// template that stopped emitting a field would satisfy an absence check
// just as well as one that correctly escaped it.
func TestRenderDiagSweepEscapesClusterContent(t *testing.T) {
	page := sweepPage(t)
	page.AllNamespaces = true
	page.Reports[0].Kind = kinds.Kind(`<script>kind</script>`)
	page.Reports[0].Namespace = `<b>ns</b>`
	page.Reports[1].Name = `<script>alert(1)</script>`
	page.Reports[1].Findings[0].Summary = `he said "boom" & <b>left</b>`

	out, err := RenderDiag(page)
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	html := string(out)

	for _, forbidden := range []string{
		"<script>kind",
		"<script>alert",
		"<b>ns</b>",
		"<b>left</b>",
	} {
		if strings.Contains(html, forbidden) {
			t.Errorf("unescaped cluster content in output: %q", forbidden)
		}
	}
	// json.Marshal's default HTML-escaping renders "<" as the six-byte
	// literal escape sequence, backslash-u-0-0-3-c, not the HTML entity
	// "&lt;" the old html/template-based rendering used. Checked against
	// that literal sequence rather than a bare "<script>" substring: every
	// page already contains real <script> tags for tabulatorJS/kxGridJS, so
	// a check for the unescaped form would pass unconditionally regardless
	// of whether this payload's escaping actually worked.
	if !strings.Contains(html, jsonEscapedLT+"script"+jsonEscapedGT) {
		t.Error("JSON payload did not HTML-escape a script tag")
	}

	rows := decodeDiagRows(t, html)
	if rows[0].Kind != `<script>kind</script>` || rows[0].Namespace != `<b>ns</b>` {
		t.Errorf("escaping mangled the decoded values instead of just neutralising them: %+v", rows[0])
	}
	if rows[1].Name != `<script>alert(1)</script>` {
		t.Errorf("escaping mangled the decoded name: %+v", rows[1])
	}
}

// A sweep that examined nothing must not claim health it never checked.
// render.Triage (internal/render/triage.go) branches on Checked == 0 before
// it ever looks at Reports, printing "Mixed · <ns> · 0 checked" with no
// health claim. Before this fix, diag.gohtml's body branched on {{if
// .Reports}} only, so a zero-Checked sweep fell into the "reports empty"
// branch alongside a genuinely all-healthy one and rendered "0 checked · all
// healthy" — a health verdict on zero examined resources.
func TestRenderDiagSweepZeroCheckedMakesNoHealthClaim(t *testing.T) {
	page := sweepPage(t)
	page.Reports = nil
	page.Checked = 0

	out, err := RenderDiag(page)
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	html := string(out)
	if strings.Contains(html, "all healthy") {
		t.Error("a sweep that checked nothing claimed everything was healthy")
	}
	if !strings.Contains(html, "0 checked") {
		t.Error("a zero-checked sweep did not report its count")
	}
}

// stripCSSComments removes every /* ... */ span. CSS comments cannot nest,
// so cutting at the first "/*"/"*/" pair repeatedly is exact; a comment with
// no closing marker (shouldn't happen in valid CSS) discards the rest of the
// string rather than looping.
func stripCSSComments(css string) string {
	var out strings.Builder
	for {
		start := strings.Index(css, "/*")
		if start < 0 {
			out.WriteString(css)
			break
		}
		out.WriteString(css[:start])
		rest := css[start+2:]
		end := strings.Index(rest, "*/")
		if end < 0 {
			break
		}
		css = rest[end+2:]
	}
	return out.String()
}

// cssRule returns the declaration block of the rule that begins with prefix,
// comments stripped first so a mention inside prose cannot satisfy a caller.
func cssRule(t *testing.T, css, prefix string) string {
	t.Helper()
	stripped := stripCSSComments(css)
	at := strings.Index(stripped, prefix)
	if at < 0 {
		t.Fatalf("stylesheet has no rule beginning %q", prefix)
	}
	end := strings.Index(stripped[at:], "}")
	if end < 0 {
		t.Fatalf("rule %q is never closed", prefix)
	}
	return stripped[at : at+end+1]
}

// style.css previously styled no anchors at all, so the CVE ID links
// scan.gohtml renders fell back to the browser's default link colour —
// roughly #0000EE, about 2:1 contrast against github-dark's #0d1117
// background and effectively invisible on nine of kx's ten palettes. The
// rule applies regardless of whether the anchor comes from server-rendered
// HTML or (as with the scan findings grid today) a DOM node kx-grid.js
// builds client-side — CSS selectors don't care which built the element.
func TestAnchorsUseThePaletteAccent(t *testing.T) {
	if !strings.Contains(stylesheet, "a { color: var(--accent); }") {
		t.Error("anchors are not coloured through the palette's accent " +
			"custom property, so links fall back to the browser's default " +
			"colour regardless of theme")
	}
}

// The masthead's plain-text "kx" wordmark was replaced with an inline SVG
// logomark so the report carries the same brand mark as the one kx --help
// prints. It must pick up the theme the same way the text wordmark did: via
// the palette's accent custom property, not a fixed color, or a theme swap
// would recolor everything else on the page except the logo.
func TestWordmarkUsesThePaletteAccent(t *testing.T) {
	out, err := RenderDiag(DiagPage{
		Meta: testMeta(t), Single: true,
		Reports: []diagnostics.Report{{
			Kind: kinds.Pod, Name: "quiet", Namespace: "diagnostics",
			Verdict: diagnostics.OK,
		}},
	})
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	if !strings.Contains(string(out), `<svg class="wordmark"`) {
		t.Error("the masthead does not render the wordmark as an svg")
	}
	rule := cssRule(t, stylesheet, ".wordmark {")
	if !strings.Contains(rule, "fill: var(--accent);") {
		t.Error(".wordmark does not fill through the palette's accent custom " +
			"property, so the masthead logo would not recolor with the theme")
	}
}

// The tab icon is the same mark, drawn from the same lines, in the palette the
// report was rendered with — a page saved under one theme and one saved under
// another are told apart in a tab strip by their icons.
//
// It has to survive html/template's URL filter, which rewrites any scheme it
// doesn't recognise to "#ZgotmplZ" and would leave every report with a broken
// icon; that is what the decode below is really checking.
func TestFaviconCarriesThePaletteAccent(t *testing.T) {
	for _, name := range []string{"github-dark", "dracula"} {
		t.Run(name, func(t *testing.T) {
			styles, err := theme.WebStyles(name)
			if err != nil {
				t.Fatalf("WebStyles returned %v", err)
			}
			meta := testMeta(t)
			meta.Styles = styles

			out, err := RenderDiag(DiagPage{
				Meta: meta, Single: true,
				Reports: []diagnostics.Report{{
					Kind: kinds.Pod, Name: "quiet", Namespace: "diagnostics",
					Verdict: diagnostics.OK,
				}},
			})
			if err != nil {
				t.Fatalf("RenderDiag returned %v", err)
			}

			_, rest, found := strings.Cut(string(out),
				`<link rel="icon" type="image/svg+xml" href="`)
			if !found {
				t.Fatalf("no favicon link in the page head; ZgotmplZ present: %v",
					strings.Contains(string(out), "ZgotmplZ"))
			}
			// html/template writes "+" as &#43; inside a URL attribute, which
			// is what a browser decodes before fetching it.
			href, _, _ := strings.Cut(rest, `"`)
			encoded, ok := strings.CutPrefix(html.UnescapeString(href),
				"data:image/svg+xml;base64,")
			if !ok {
				t.Fatalf("favicon href is not an inline svg data URI: %s", href)
			}
			svg, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				t.Fatalf("favicon is not decodable base64: %v", err)
			}
			if !strings.Contains(string(svg), `fill="`+styles[theme.Accent]+`"`) {
				t.Errorf("favicon is not filled with %s's accent %s: %s",
					name, styles[theme.Accent], svg)
			}
			if !strings.Contains(string(svg), "<rect") {
				t.Errorf("favicon carries no shapes: %s", svg)
			}
		})
	}
}

// Every report shape is a browser tab, so every one of them needs the icon —
// the head is shared, and this is what says so: a page type rendered through
// some other layout would be the one tab still showing the browser's blank
// default.
func TestEveryReportCarriesTheFavicon(t *testing.T) {
	pages := map[string]func() ([]byte, error){
		"diag": func() ([]byte, error) {
			return RenderDiag(DiagPage{
				Meta: testMeta(t), Single: true,
				Reports: []diagnostics.Report{criticalReport(t)},
			})
		},
		"scan": func() ([]byte, error) { return RenderScan(scanPage(t)) },
		"tree": func() ([]byte, error) {
			return RenderTree(TreePage{Meta: testMeta(t), Scope: "prod", Root: ownershipTree()})
		},
		"top": func() ([]byte, error) {
			return RenderTop(TopPage{
				Meta: testMeta(t), Scope: "prod",
				Rows: []TopRow{{Index: 1, Name: "api-gateway-7d4f9c-2xk8p", CPU: "12m", Memory: "466Mi"}},
			})
		},
	}
	for name, render := range pages {
		t.Run(name, func(t *testing.T) {
			out, err := render()
			if err != nil {
				t.Fatalf("render returned %v", err)
			}
			if !strings.Contains(string(out), `<link rel="icon" type="image/svg+xml"`) {
				t.Error("page has no favicon link in its head")
			}
		})
	}
}

func scanPage(t *testing.T) ScanPage {
	t.Helper()
	return ScanPage{
		Meta: testMeta(t), Scope: "diagnostics",
		Images: []scanner.ImageScan{
			{
				Image:  "ghcr.io/jzills/api-gateway:1.8.3",
				Counts: map[string]int{"CRITICAL": 1, "HIGH": 2, "MEDIUM": 0, "LOW": 1, "UNSPECIFIED": 0},
				Findings: []scanner.Finding{
					{ID: "CVE-2021-22945", Severity: "CRITICAL", Package: "curl",
						Installed: "7.74.0-1.3+deb11u1", FixedIn: "7.74.0-1.3+deb11u2",
						URL: "https://scout.docker.com/v/CVE-2021-22945"},
					{ID: "CVE-2023-44487", Severity: "LOW", Package: "nginx",
						Installed: "1.21.6-1~bullseye"},
				},
			},
			{Image: "ghcr.io/jzills/ledger:0.4.0", Error: "manifest unknown"},
		},
	}
}

// "Mixed · " is the cross-kind sweep label (see diag.gohtml's own "Mixed ·
// {{.Scope}}" caption, which appears only in its sweep branch). An indexed
// scan's kind is already known and printed right beside it in Scope
// (cli/scan.go's pageScope), so the template must render Scope verbatim
// rather than assuming every scan is a sweep. Every scan fixture in this file
// otherwise uses Scope: "diagnostics" — a bare namespace indistinguishable
// from a real sweep's own scope string — which is why a hardcoded "Mixed · "
// prefix here went uncaught until an indexed-shaped Scope was tried.
func TestRenderScanCaptionRendersScopeVerbatim(t *testing.T) {
	page := scanPage(t)
	page.Scope = "Deployment/web · prod"

	out, err := RenderScan(page)
	if err != nil {
		t.Fatalf("RenderScan returned %v", err)
	}
	html := string(out)
	if strings.Contains(html, "Mixed") {
		t.Error("an indexed scan's page caption said \"Mixed\", the cross-kind sweep label")
	}
	if !strings.Contains(html, `<p class="caption">Deployment/web · prod ·`) {
		t.Error("the page caption did not render the indexed scope verbatim")
	}
}

func TestRenderScanListsImagesAndCVEs(t *testing.T) {
	out, err := RenderScan(scanPage(t))
	if err != nil {
		t.Fatalf("RenderScan returned %v", err)
	}
	html := string(out)

	images := decodeScanImageRows(t, html)
	if len(images) != 2 || images[0].Image != "ghcr.io/jzills/api-gateway:1.8.3" {
		t.Fatalf("image grid payload missing the fixture's images: %+v", images)
	}

	findings := decodeScanFindingRows(t, html)
	var got *ScanFindingRow
	for i := range findings {
		if findings[i].ID == "CVE-2021-22945" {
			got = &findings[i]
		}
	}
	if got == nil {
		t.Fatal("findings payload is missing CVE-2021-22945")
	}
	if got.Package != "curl" || got.Installed != "7.74.0-1.3+deb11u1" || got.FixedIn != "7.74.0-1.3+deb11u2" {
		t.Errorf("CVE-2021-22945 did not round-trip its package/installed/fixed fields: %+v", got)
	}
	if got.URL != "https://scout.docker.com/v/CVE-2021-22945" {
		t.Errorf("CVE-2021-22945 lost its URL: %+v", got)
	}
	if !got.Fixable {
		t.Error("a CVE with a FixedIn value must be Fixable")
	}
}

// The dash itself is drawn client-side (fixedInFormatter in kx-grid.js,
// verified manually per the package's testing strategy); what the server
// controls, and what this pins, is that an unfixed CVE's FixedIn genuinely
// comes through empty rather than some placeholder text the formatter would
// have to special-case.
func TestRenderScanShowsDashWhenUnfixed(t *testing.T) {
	out, err := RenderScan(scanPage(t))
	if err != nil {
		t.Fatalf("RenderScan returned %v", err)
	}
	findings := decodeScanFindingRows(t, string(out))
	for _, f := range findings {
		if f.ID == "CVE-2023-44487" {
			if f.FixedIn != "" || f.Fixable {
				t.Errorf("an unfixed CVE reported FixedIn=%q Fixable=%v", f.FixedIn, f.Fixable)
			}
			return
		}
	}
	t.Fatal("findings payload is missing CVE-2023-44487")
}

// A failed scan keeps its message and must not claim real counts for an
// image that was never actually scanned — reading a severity out of a nil
// Counts map returns Go's int zero value rather than panicking, so a
// regression here would silently report a clean 0/0/0/0/0 image instead of
// surfacing the failure. Dashing those zeros out in the UI is kx-grid.js's
// job (countFormatter checks Error before trusting a count); this pins the
// data it depends on.
func TestRenderScanKeepsFailureMessages(t *testing.T) {
	out, err := RenderScan(scanPage(t))
	if err != nil {
		t.Fatalf("RenderScan returned %v", err)
	}
	html := string(out)
	if !strings.Contains(html, "manifest unknown") {
		t.Error("the failure message is missing from the JSON payload")
	}

	images := decodeScanImageRows(t, html)
	for _, img := range images {
		if img.Image == "ghcr.io/jzills/ledger:0.4.0" {
			if img.Error != "manifest unknown" {
				t.Errorf("failed image lost its Error field: %+v", img)
			}
			if img.Critical != 0 || img.High != 0 || img.Medium != 0 || img.Low != 0 || img.Unspecified != 0 {
				t.Errorf("a failed scan reported nonzero counts: %+v", img)
			}
			return
		}
	}
	t.Fatal("image payload is missing the failed image")
}

// Image names and scanner errors both come from outside kx.
func TestRenderScanEscapesImageNames(t *testing.T) {
	page := scanPage(t)
	page.Images[1].Image = `<script>alert(1)</script>`
	page.Images[1].Error = `<b>nope</b>`

	out, err := RenderScan(page)
	if err != nil {
		t.Fatalf("RenderScan returned %v", err)
	}
	html := string(out)
	if strings.Contains(html, "<script>alert(1)") || strings.Contains(html, "<b>nope</b>") {
		t.Error("unescaped scanner content reached the page")
	}

	images := decodeScanImageRows(t, html)
	if images[1].Image != `<script>alert(1)</script>` || images[1].Error != `<b>nope</b>` {
		t.Errorf("escaping mangled the decoded image fields: %+v", images[1])
	}
}

// ID/Package/Installed must never reach the page as literal HTML
// metacharacters. URL is deliberately NOT checked for absence here: unlike
// those three, a raw URL value is expected to survive intact in the inert
// JSON blob — it only becomes dangerous once turned into a real href, which
// now happens client-side (kx-grid.js's cveFormatter, guarded by
// isSafeLink's http(s)-only allowlist, mirroring html/template's own
// "#ZgotmplZ" neutralisation of dangerous URL schemes in the pre-grid
// rendering). No Go test executes that JS; it's covered by code review and
// the manual QA pass described in the plan, not here.
func TestRenderScanEscapesFindingFields(t *testing.T) {
	page := ScanPage{
		Meta: testMeta(t), Scope: "diagnostics",
		Images: []scanner.ImageScan{{
			Image:  "ghcr.io/jzills/api-gateway:1.8.3",
			Counts: map[string]int{"CRITICAL": 1, "HIGH": 0, "MEDIUM": 0, "LOW": 0, "UNSPECIFIED": 0},
			Findings: []scanner.Finding{{
				ID:        `<script>alert("id")</script>`,
				Severity:  "CRITICAL",
				Package:   `<b>package</b>`,
				Installed: `<i>installed</i>`,
				URL:       `javascript:alert(1)`,
			}},
		}},
	}
	out, err := RenderScan(page)
	if err != nil {
		t.Fatalf("RenderScan returned %v", err)
	}
	html := string(out)

	for _, forbidden := range []string{"<script>alert", "<b>package</b>", "<i>installed</i>"} {
		if strings.Contains(html, forbidden) {
			t.Errorf("unescaped cluster content reached the page: %q", forbidden)
		}
	}

	findings := decodeScanFindingRows(t, html)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	got := findings[0]
	if got.ID != `<script>alert("id")</script>` || got.Package != `<b>package</b>` || got.Installed != `<i>installed</i>` {
		t.Errorf("escaping mangled the decoded finding fields: %+v", got)
	}
	if got.URL != `javascript:alert(1)` {
		t.Errorf("the raw URL did not round-trip: %+v", got)
	}
}

// scanner.Severities has five buckets, not four: CountBySeverity folds any
// finding ParseFindings could not map to a known severity into UNSPECIFIED,
// deliberately, so it isn't dropped from the total (internal/scanner/scanner.go).
// render.ScanSummary (internal/render/scan.go) has always had a UNSPEC
// column for exactly this reason, and page.go's own package doc says the
// page and terminal must not disagree about severity.
func TestRenderScanShowsUnspecifiedCounts(t *testing.T) {
	page := ScanPage{
		Meta: testMeta(t), Scope: "diagnostics",
		Images: []scanner.ImageScan{{
			Image:  "ghcr.io/jzills/mystery:2.1.0",
			Counts: map[string]int{"CRITICAL": 0, "HIGH": 0, "MEDIUM": 0, "LOW": 0, "UNSPECIFIED": 3},
			Findings: []scanner.Finding{
				{ID: "CVE-2024-00001", Severity: "UNSPECIFIED", Package: "libfoo", Installed: "1.0"},
				{ID: "CVE-2024-00002", Severity: "UNSPECIFIED", Package: "libbar", Installed: "2.0"},
				{ID: "CVE-2024-00003", Severity: "UNSPECIFIED", Package: "libbaz", Installed: "3.0"},
			},
		}},
	}
	out, err := RenderScan(page)
	if err != nil {
		t.Fatalf("RenderScan returned %v", err)
	}
	html := string(out)

	images := decodeScanImageRows(t, html)
	if images[0].Unspecified != 3 {
		t.Errorf("an image with only UNSPECIFIED findings did not carry its count: %+v", images[0])
	}
	findings := decodeScanFindingRows(t, html)
	if len(findings) != 3 {
		t.Fatalf("got %d findings, want 3", len(findings))
	}
}

// A clean image gets no severity bands (severityBar's zero-total special
// case: dividing by a zero total would otherwise panic) and contributes no
// rows to the findings grid at all. Its clean state is communicated by the
// image grid's own zero counts; unlike the old per-image <details> drawer,
// there is no separate per-image empty-state message in the findings grid
// (a flat, cross-image view has no natural per-image slot for one).
func TestRenderScanHealthyImageIsEmptyNotZero(t *testing.T) {
	page := ScanPage{
		Meta: testMeta(t), Scope: "diagnostics",
		Images: []scanner.ImageScan{{
			Image:  "ghcr.io/jzills/quiet:1.0.0",
			Counts: map[string]int{"CRITICAL": 0, "HIGH": 0, "MEDIUM": 0, "LOW": 0, "UNSPECIFIED": 0},
		}},
	}
	out, err := RenderScan(page)
	if err != nil {
		t.Fatalf("RenderScan returned %v", err)
	}
	html := string(out)

	images := decodeScanImageRows(t, html)
	if len(images[0].Bar) != 0 {
		t.Errorf("a clean image rendered severity bands: %+v", images[0].Bar)
	}
	findings := decodeScanFindingRows(t, html)
	if len(findings) != 0 {
		t.Errorf("a clean image contributed rows to the findings grid: %+v", findings)
	}
}

// ownershipTree builds a small three-level graph — a header-styled root with
// one indexed, non-leaf child and one indexed leaf grandchild — so a single
// fixture exercises the nesting, header-style and index behaviour together,
// the way a real `kx tree` walk would nest them.
func ownershipTree() *tree.Node {
	return &tree.Node{
		Label: "Deployment/web", Style: theme.Header,
		Children: []*tree.Node{{
			Label: "rs/web-abc", Style: theme.Accent, Index: 1,
			Children: []*tree.Node{
				{Label: "pod/web-abc-1", Style: theme.Body, Index: 2},
			},
		}},
	}
}

// theme.Header carries no CSS custom property of its own — headers are
// bold-accent, not their own colour (theme.WebStyles emits no --header var)
// — so kx-grid.js's treeLabelFormatter must route it through --accent rather
// than a nonexistent var(--header), the same constraint the old template
// held.
func TestRenderTreeHeaderStyleIsBoldAccent(t *testing.T) {
	out, err := RenderTree(TreePage{Meta: testMeta(t), Scope: "prod", Root: ownershipTree()})
	if err != nil {
		t.Fatalf("RenderTree returned %v", err)
	}
	roots := decodeTreeRows(t, string(out))
	if roots[0].Style != theme.Header {
		t.Errorf("the root's Style did not round-trip as %q: got %q", theme.Header, roots[0].Style)
	}

	rule := cssRule(t, stylesheet, ".tree-label.style-header {")
	if !strings.Contains(rule, "var(--accent)") {
		t.Error(".tree-label.style-header does not resolve through var(--accent)")
	}
	if strings.Contains(rule, "var(--header)") {
		t.Error(".tree-label.style-header references a nonexistent var(--header)")
	}
}

func TestRenderTreeIndexPrefix(t *testing.T) {
	out, err := RenderTree(TreePage{Meta: testMeta(t), Scope: "prod", Root: ownershipTree()})
	if err != nil {
		t.Fatalf("RenderTree returned %v", err)
	}
	roots := decodeTreeRows(t, string(out))
	child := roots[0].Children[0]
	if child.Index != 1 {
		t.Errorf("an indexed node did not round-trip its index: got %d, want 1", child.Index)
	}
	grandchild := child.Children[0]
	if grandchild.Index != 2 {
		t.Errorf("an indexed leaf did not round-trip its index: got %d, want 2", grandchild.Index)
	}

	unindexed := &tree.Node{
		Label: "Deployment/web", Style: theme.Header,
		Children: []*tree.Node{
			{Label: "rs/web-abc", Style: theme.Accent,
				Children: []*tree.Node{{Label: "pod/web-abc-1", Style: theme.Body}}},
		},
	}
	out, err = RenderTree(TreePage{Meta: testMeta(t), Scope: "prod", Root: unindexed})
	if err != nil {
		t.Fatalf("RenderTree returned %v", err)
	}
	roots = decodeTreeRows(t, string(out))
	if roots[0].Children[0].Index != 0 {
		t.Error("an unindexed tree rendered a nonzero index")
	}
}

// Resource names are cluster-controlled, same protection
// TestRenderDiagSweepEscapesClusterContent pins for diag's sweep grid.
func TestRenderTreeEscapesClusterContent(t *testing.T) {
	root := &tree.Node{Label: `<script>alert(1)</script>`, Style: theme.Header}
	out, err := RenderTree(TreePage{Meta: testMeta(t), Scope: "prod", Root: root})
	if err != nil {
		t.Fatalf("RenderTree returned %v", err)
	}
	html := string(out)
	if strings.Contains(html, "<script>alert") {
		t.Error("unescaped cluster content in output")
	}
	if !strings.Contains(html, jsonEscapedLT+"script"+jsonEscapedGT) {
		t.Error("JSON payload did not HTML-escape a script tag")
	}
	roots := decodeTreeRows(t, html)
	if roots[0].Label != `<script>alert(1)</script>` {
		t.Errorf("escaping mangled the decoded label: %+v", roots[0])
	}
}

// The palette reaches the page as custom properties, so kx theme changes the
// browser output — parity with TestRenderDiagUsesTheActivePalette.
func TestRenderTreeUsesTheActivePalette(t *testing.T) {
	styles, err := theme.WebStyles("dracula")
	if err != nil {
		t.Fatalf("WebStyles returned %v", err)
	}
	meta := testMeta(t)
	meta.Styles = styles

	out, err := RenderTree(TreePage{Meta: meta, Scope: "prod", Root: ownershipTree()})
	if err != nil {
		t.Fatalf("RenderTree returned %v", err)
	}
	if !strings.Contains(string(out), "--background:#282a36;") {
		t.Error("dracula's background did not reach the page")
	}
}

// An -A sweep renders one root per namespace in the JSON array Tabulator's
// dataTree mode reads — no separate markup shape is needed for the forest
// case.
func TestRenderTreeAllNamespacesRendersEveryRoot(t *testing.T) {
	roots := []*tree.Node{
		{Label: "Namespace/default", Style: theme.Header,
			Children: []*tree.Node{{Label: "Deployment/web", Style: theme.Header}}},
		{Label: "Namespace/prod", Style: theme.Header,
			Children: []*tree.Node{{Label: "Deployment/api", Style: theme.Header}}},
	}
	out, err := RenderTree(TreePage{
		Meta: testMeta(t), Scope: "all namespaces",
		AllNamespaces: true, Roots: roots,
	})
	if err != nil {
		t.Fatalf("RenderTree returned %v", err)
	}
	html := string(out)
	if !strings.Contains(html, "all namespaces") {
		t.Error("scope caption missing")
	}

	decoded := decodeTreeRows(t, html)
	if len(decoded) != 2 {
		t.Fatalf("got %d roots, want 2", len(decoded))
	}
	if decoded[0].Label != "Namespace/default" || decoded[0].Children[0].Label != "Deployment/web" {
		t.Errorf("first root did not round-trip: %+v", decoded[0])
	}
	if decoded[1].Label != "Namespace/prod" || decoded[1].Children[0].Label != "Deployment/api" {
		t.Errorf("second root did not round-trip: %+v", decoded[1])
	}
}

// The AllNamespaces flag must actually gate which field the template reads:
// a Root left over on a mis-set page must not silently render instead of the
// intended empty Roots slice, and vice versa.
func TestRenderTreeAllNamespacesIgnoresRoot(t *testing.T) {
	out, err := RenderTree(TreePage{
		Meta: testMeta(t), Scope: "all namespaces", AllNamespaces: true,
		Root:  &tree.Node{Label: "Namespace/wrong-field", Style: theme.Header},
		Roots: []*tree.Node{{Label: "Namespace/right-field", Style: theme.Header}},
	})
	if err != nil {
		t.Fatalf("RenderTree returned %v", err)
	}
	html := string(out)
	if strings.Contains(html, "wrong-field") {
		t.Error("an AllNamespaces page rendered Root instead of Roots")
	}
	decoded := decodeTreeRows(t, html)
	if len(decoded) != 1 || decoded[0].Label != "Namespace/right-field" {
		t.Errorf("an AllNamespaces page did not render Roots: %+v", decoded)
	}
}

// Unlike RenderDiag/RenderScan, RenderTree binds no clock-derived state, so
// two renders of the same page value must produce identical bytes with no
// time-travel setup needed to prove it.
func TestRenderTreeIsByteStable(t *testing.T) {
	page := TreePage{Meta: testMeta(t), Scope: "prod", Root: ownershipTree()}

	first, err := RenderTree(page)
	if err != nil {
		t.Fatalf("RenderTree returned %v", err)
	}
	second, err := RenderTree(page)
	if err != nil {
		t.Fatalf("RenderTree returned %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Error("two renders of the same page produced different bytes")
	}
}

func decodeTopRows(t *testing.T, html string) []TopRow {
	t.Helper()
	var rows []TopRow
	raw := extractJSONScript(t, html, "kx-top-data")
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatalf("could not decode top JSON payload: %v\nraw: %s", err, raw)
	}
	return rows
}

func TestRenderTopRendersRowsAndScope(t *testing.T) {
	page := TopPage{
		Meta:  testMeta(t),
		Scope: "Pods · prod",
		Rows: []TopRow{
			{Index: 1, Name: "web-1", CPU: "5m", Memory: "64Mi",
				CPUPct: NewUsage(12, "cpu"), MemPct: NewUsage(80, "memory")},
		},
	}
	out, err := RenderTop(page)
	if err != nil {
		t.Fatalf("RenderTop returned %v", err)
	}
	html := string(out)
	if !strings.Contains(html, "Pods · prod") {
		t.Error("scope caption missing")
	}
	rows := decodeTopRows(t, html)
	if len(rows) != 1 || rows[0].Name != "web-1" {
		t.Fatalf("rows did not round-trip: %+v", rows)
	}
	if !rows[0].MemPct.Known || rows[0].MemPct.Pct != 80 {
		t.Errorf("MemPct = %+v, want Known with Pct 80", rows[0].MemPct)
	}
	if rows[0].MemPct.Class == "" {
		t.Errorf("MemPct.Class is empty, want a severity class for 80%%")
	}
}

// Unlike RenderDiag/RenderScan, RenderTop binds no clock-derived state (no
// "age" is used on a top page), so two renders of the same page value must
// produce identical bytes with no time-travel setup needed to prove it —
// the same guarantee TestRenderTreeIsByteStable pins for RenderTree.
func TestRenderTopIsByteStable(t *testing.T) {
	page := TopPage{
		Meta: testMeta(t), Scope: "Nodes · default",
		Rows: []TopRow{{Index: 1, Name: "node-a", CPU: "196m", Memory: "1864Mi",
			CPUPct: NewUsage(1, "cpu"), MemPct: NewUsage(24, "memory")}},
	}
	first, err := RenderTop(page)
	if err != nil {
		t.Fatalf("RenderTop returned %v", err)
	}
	second, err := RenderTop(page)
	if err != nil {
		t.Fatalf("RenderTop returned %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Error("two renders of the same page produced different bytes")
	}
}

func TestNewUsageIsAlwaysKnown(t *testing.T) {
	// Sanity check that NewUsage always reports Known (it takes an
	// already-computed percentage, unlike usageOf which can return an
	// unknown Usage{} when the limit itself is missing).
	u := NewUsage(50, "cpu")
	if !u.Known || u.Pct != 50 {
		t.Errorf("NewUsage(50, cpu) = %+v, want Known with Pct 50", u)
	}
}
