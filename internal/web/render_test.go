package web

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/scanner"
	"github.com/jzills/kx/internal/theme"
)

func testMeta(t *testing.T) Meta {
	t.Helper()
	styles, err := theme.WebStyles(theme.Default)
	if err != nil {
		t.Fatalf("WebStyles returned %v", err)
	}
	return Meta{
		Title:      "kx diag",
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
// print into its own logs must not become markup.
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
		Checked: 14, Healthy: 12,
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
		"12 healthy resources not shown",
		`<span class="index">1</span>`,
		`<span class="index">2</span>`,
		"storage-pending",
		"Volume is Pending",
		// The drill-down is the same report block, so the detail is present.
		"CrashLoopBackOff",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("sweep page is missing %q", want)
		}
	}
	if !strings.Contains(html, "<details") {
		t.Error("sweep rows are not expandable")
	}
}

// A cluster-wide sweep saves no state, so there are no indexes to print. The
// namespace takes the column instead, matching render.Triage.
func TestRenderDiagAllNamespacesSwapsIndexForNamespace(t *testing.T) {
	page := sweepPage(t)
	page.AllNamespaces = true
	page.Scope = "all namespaces"

	out, err := RenderDiag(page)
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	html := string(out)

	if strings.Contains(html, `<span class="index">1</span>`) {
		t.Error("an all-namespaces sweep printed indexes")
	}
	if !strings.Contains(html, "NAMESPACE") {
		t.Error("an all-namespaces sweep did not print a namespace column")
	}
	if !strings.Contains(html, "all namespaces") {
		t.Error("scope caption missing")
	}
}

func TestRenderDiagSweepAllHealthy(t *testing.T) {
	page := sweepPage(t)
	page.Reports = nil
	page.Healthy = 14

	out, err := RenderDiag(page)
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	if !strings.Contains(string(out), "all healthy") {
		t.Error("an all-healthy sweep did not say so")
	}
}

// A name collision drops a row from the indexed listing; the page has to say
// which, or the absence looks like a bug.
func TestRenderDiagSweepReportsDroppedRows(t *testing.T) {
	page := sweepPage(t)
	page.Dropped = []string{"Service/api-gateway"}

	out, err := RenderDiag(page)
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	if !strings.Contains(string(out), "Service/api-gateway") {
		t.Error("dropped row was not reported")
	}
}

// The -A layout emits KIND and NAMESPACE where the indexed layout emits an
// index and a kind, so it needs its own column widths. Without the modifier
// class the kind renders into a slot sized for a two-digit index.
func TestRenderDiagAllNamespacesUsesItsOwnGrid(t *testing.T) {
	page := sweepPage(t)
	page.AllNamespaces = true

	out, err := RenderDiag(page)
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	html := string(out)
	// Checked via the literal class attribute, not a bare "ns-grid"
	// substring: the embedded stylesheet's own selector
	// (".sweep-head.ns-grid") also contains that text, so a bare substring
	// check can't tell a class actually applied to an element from the CSS
	// rule that targets it. The header and a row are checked separately, via
	// their full class attributes, so a fix that applies the modifier to
	// only one of the two still fails here.
	if !strings.Contains(html, `class="sweep-head ns-grid"`) {
		t.Error("the -A header did not get its own grid")
	}
	if !strings.Contains(html, `class="row status-bad ns-grid"`) {
		t.Error("the -A rows did not get their own grid")
	}

	// The indexed layout must NOT pick it up. Checking for the quoted
	// occurrence (an HTML attribute value ending in ns-grid) rather than the
	// bare word, again to avoid matching the stylesheet's own CSS selectors,
	// which contain no quote characters.
	indexed, err := RenderDiag(sweepPage(t))
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	if strings.Contains(string(indexed), `ns-grid"`) {
		t.Error("the indexed layout wrongly used the -A grid")
	}
}

// Kind, Namespace, Name, finding summaries and dropped-row names are all
// cluster-controlled, and this task put them in new markup — the sweep
// table, the <details><summary> row, and the footer. They must be escaped
// exactly like the single-resource view's TestRenderDiagEscapesClusterContent
// already requires. AllNamespaces is set because that is the branch that
// prints Namespace at all.
func TestRenderDiagSweepEscapesClusterContent(t *testing.T) {
	page := sweepPage(t)
	page.AllNamespaces = true
	page.Reports[0].Kind = kinds.Kind(`<script>kind</script>`)
	page.Reports[0].Namespace = `<b>ns</b>`
	page.Reports[1].Name = `<script>alert(1)</script>`
	page.Reports[1].Findings[0].Summary = `he said "boom" & <b>left</b>`
	page.Dropped = []string{`<script>dropped</script>`}

	out, err := RenderDiag(page)
	if err != nil {
		t.Fatalf("RenderDiag returned %v", err)
	}
	html := string(out)

	for _, forbidden := range []string{
		"<script>kind",
		"<script>alert",
		"<script>dropped",
		"<b>ns</b>",
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
	page.Healthy = 0

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

// mediaQueryBlock returns the declaration text inside the first rule whose
// prelude contains query, matched by counting braces rather than by a
// regexp, so nested rules inside the block (there are two) don't end the
// scan early. Scoping to the block matters: ".ns-grid" and ".scan-grid"
// already appear elsewhere in the stylesheet's desktop rules, so a check
// against the whole file would pass whether or not the mobile rule actually
// mentions them.
func mediaQueryBlock(t *testing.T, css, query string) string {
	t.Helper()
	start := strings.Index(css, query)
	if start < 0 {
		t.Fatalf("stylesheet does not contain %q", query)
	}
	relOpen := strings.IndexByte(css[start:], '{')
	if relOpen < 0 {
		t.Fatalf("no opening brace found after %q", query)
	}
	open := start + relOpen

	depth := 0
	for i := open; i < len(css); i++ {
		switch css[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return css[open+1 : i]
			}
		}
	}
	t.Fatalf("no matching closing brace found for the %q block", query)
	return ""
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

// gridCollapseSelectors returns the selector list of the mobile collapse
// rule — the first rule inside @media (max-width: 720px), the one setting
// grid-template-columns — with comments stripped first and the declaration
// body (after the first "{") cut off, so a check against it can only be
// satisfied by the selectors themselves.
//
// Both steps matter, and a prior round of this test skipped both: the rule
// has its own explanatory comment describing why ".ns-grid" and
// ".scan-grid" must be repeated here, and that comment contains those exact
// two substrings. Scoping to the media-query block (mediaQueryBlock, above)
// is not enough on its own — the comment sits inside that block too — so a
// check against the raw block passed whether or not the selector list
// itself named either modifier. The reviewer's targeted mutation (delete
// just the selectors, keep the comment) is what exposed this; a whole-file
// revert to pre-task HEAD had removed the comment along with everything
// else, which is why it looked like conclusive mutation evidence at the
// time and wasn't.
func gridCollapseSelectors(t *testing.T, css string) string {
	t.Helper()
	block := stripCSSComments(mediaQueryBlock(t, css, "@media (max-width: 720px)"))
	brace := strings.IndexByte(block, '{')
	if brace < 0 {
		t.Fatal("the mobile collapse rule has no declaration block")
	}
	return block[:brace]
}

// desktopCSS returns the stylesheet with every comment stripped and the
// mobile collapse's whole @media block cut away, leaving only the rules that
// apply at every viewport width. Comments are stripped from the FULL
// stylesheet first — not just inside the media block — because the desktop
// ".scan-grid" rule has its own explanatory comment (immediately above it in
// style.css) that names ".scan-grid" and ".row.scan-grid > summary" in
// prose; without stripping it, a check for those selectors could pass
// against the comment alone with the rule itself deleted, the same class of
// gap gridCollapseSelectors' own doc comment describes. @media (max-width:
// 720px) is the last rule in style.css, so cutting the text at its start
// leaves exactly the desktop-only rules before it; TestMobileCollapseCoversNsGrid
// and TestMobileCollapseExcludesScanGrid already establish the media query's
// own selector list separately, so there is no need to also exclude its
// interior here beyond the cut.
// cssRule returns the declaration block of the rule that begins with prefix,
// comments stripped first so a mention inside prose cannot satisfy a caller —
// the same trap desktopCSS strips comments to avoid.
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

func desktopCSS(t *testing.T, css string) string {
	t.Helper()
	stripped := stripCSSComments(css)
	at := strings.Index(stripped, "@media (max-width: 720px)")
	if at < 0 {
		t.Fatal("stylesheet does not contain the mobile collapse's @media rule")
	}
	return stripped[:at]
}

// Nothing else in this file pins that the DESKTOP ".scan-grid" rule still
// exists: TestMobileCollapseExcludesScanGrid only looks inside the mobile
// @media block, where .scan-grid is deliberately absent. Deleting the
// desktop rule entirely would leave every test in this package green while
// the scan page silently fell back to the diag sweep's six-column grid with
// eight cells, at every viewport width, not just under 720px.
func TestDesktopScanGridRuleExists(t *testing.T) {
	desktop := desktopCSS(t, stylesheet)
	if !strings.Contains(desktop, ".row > summary.scan-grid") ||
		!strings.Contains(desktop, ".sweep-head.scan-grid") {
		t.Error("the desktop .scan-grid rule (.row > summary.scan-grid, " +
			".sweep-head.scan-grid) is missing outside the mobile media " +
			"query — the scan page would silently fall back to diag's grid")
	}
}

// Same gap, -A sweep side: TestMobileCollapseCoversNsGrid only pins that the
// mobile rule's selector list repeats the ".ns-grid" modifier, not that the
// desktop rule it is repeating alongside still exists at all.
func TestDesktopNsGridRuleExists(t *testing.T) {
	desktop := desktopCSS(t, stylesheet)
	if !strings.Contains(desktop, ".row.ns-grid > summary") ||
		!strings.Contains(desktop, ".sweep-head.ns-grid") {
		t.Error("the desktop .ns-grid rule (.row.ns-grid > summary, " +
			".sweep-head.ns-grid) is missing outside the mobile media query " +
			"— the -A sweep would silently fall back to the indexed grid")
	}
}

// .event-msg must declare no colour of its own. It shares its element with a
// .status-* class on a failed scan (<p class="event-msg status-warn">), and
// both are single-class selectors, so whichever is written later wins. When
// .event-msg carried "color: var(--body)" it sat later in the sheet and the
// error message rendered in body colour, silently dropping the warning colour
// the markup asks for.
func TestEventMsgDeclaresNoColour(t *testing.T) {
	rule := cssRule(t, stylesheet, ".event-msg {")
	if strings.Contains(rule, "color:") {
		t.Errorf(".event-msg declares a colour (%q); it must inherit, or it "+
			"outranks the .status-* class beside it on a failed scan row", rule)
	}
}

// A failed scan's message must actually reach the page in the warning colour.
// The rule above is the mechanism; this is the outcome.
func TestRenderScanFailureMessageKeepsItsWarningClass(t *testing.T) {
	out, err := RenderScan(scanPage(t))
	if err != nil {
		t.Fatalf("RenderScan returned %v", err)
	}
	if !strings.Contains(string(out), `<p class="event-msg status-warn">`) {
		t.Error("the failed scan's message lost its status-warn class")
	}
}

// Carried defect: .row.ns-grid > summary is a compound selector (two classes
// plus a type), giving it higher specificity than the plain ".row >
// summary, .sweep-head" rule inside @media (max-width: 720px) — and @media
// contributes no specificity of its own. Unless the mobile rule repeats the
// modifier at the same specificity, the desktop grid wins the cascade under
// 720px and the -A sweep never collapses to the narrow layout.
//
// This checks the necessary condition — that the selector list inside the
// media query actually names the modifier — which is what a fix of this
// kind changes. It cannot check the sufficient one, that the mobile rule
// then wins the cascade in an actual browser at an actual viewport width:
// that is a rendering question no Go string comparison can answer, and
// nothing in this package parses or computes CSS specificity outside this
// test. Reasoning about the specificity arithmetic (above, and in the CSS
// comment beside the rule) is what stands in for that.
func TestMobileCollapseCoversNsGrid(t *testing.T) {
	selectors := gridCollapseSelectors(t, stylesheet)
	if !strings.Contains(selectors, ".ns-grid") {
		t.Error("the mobile collapse rule's selector list does not mention " +
			".ns-grid, so it loses the specificity contest against the " +
			"desktop rule and never collapses under 720px")
	}
}

// The scan page deliberately does not join the diag sweep's mobile
// collapse — see the comment beside .scan-grid's desktop rule in style.css.
// A scan row's columns (image, severity stack, five counts) have no pair as
// droppable as the diag sweep's KIND/FINDING, so forcing them into the same
// 3-column template would wrap the extra cells onto additional implicit
// rows rather than collapsing sensibly; the scan page keeps its desktop
// grid at every width and relies on .table-wrap's own horizontal scroll
// instead, the same mechanism the CVE detail table inside each row's drawer
// already depends on unconditionally.
//
// This pins that decision the same way TestMobileCollapseCoversNsGrid pins
// the opposite one for .ns-grid: if a later change added ".scan-grid" back
// into this selector list, the scan page would silently inherit the
// diag-shaped collapse.
func TestMobileCollapseExcludesScanGrid(t *testing.T) {
	selectors := gridCollapseSelectors(t, stylesheet)
	if strings.Contains(selectors, ".scan-grid") {
		t.Error("the mobile collapse rule's selector list mentions " +
			".scan-grid, which would force the scan page into the " +
			"diag-shaped 3-column collapse instead of its own grid plus " +
			"horizontal scroll")
	}
}

// .row's own colour must be set (var(--body)), and declared textually AFTER
// the .status-bad/.status-warn/.status-ok/.status-neutral rules it ties with
// at equal specificity (0,1,0): a CSS specificity tie is broken by source
// order, later wins. Without this, a severity class on a row (<details
// class="row status-bad">, diag.gohtml/scan.gohtml) tints every descendant
// that has no colour rule of its own — CVE package/installed cells, pod
// logs, finding summaries — via inheritance from the <details> element
// itself, contradicting those same descendants' own per-row classification
// (e.g. a LOW-severity CVE rendering red inside a row with one CRITICAL
// finding).
//
// This can only be checked textually, the same way TestMobileCollapseCoversNsGrid
// pins a specificity contest no Go test here can render an actual cascade
// for: reasoning about the arithmetic (also recorded in style.css's own
// comments beside the similar .scan-grid/.ns-grid rules) stands in for it.
func TestRowSetsItsOwnColorAfterSeverityClasses(t *testing.T) {
	rowAt := strings.Index(stylesheet, ".row {")
	if rowAt < 0 {
		t.Fatal(".row rule not found in the stylesheet")
	}
	badAt := strings.Index(stylesheet, ".status-bad {")
	if badAt < 0 {
		t.Fatal(".status-bad rule not found in the stylesheet")
	}
	if rowAt < badAt {
		t.Error(".row is declared before .status-bad in the stylesheet; at " +
			"equal specificity the severity colour would win the cascade on " +
			"the row itself and bleed into every uncoloured descendant")
	}

	end := strings.Index(stylesheet[rowAt:], "}")
	if end < 0 {
		t.Fatal(".row rule has no closing brace")
	}
	rowRule := stylesheet[rowAt : rowAt+end]
	if !strings.Contains(rowRule, "color: var(--body)") {
		t.Error(".row does not set its own color to var(--body), so a " +
			"severity-classed row still tints every descendant with no " +
			"colour rule of its own")
	}
}

// style.css previously styled no anchors at all, so the CVE ID links
// scan.gohtml renders (<a href="{{.URL}}">) fell back to the browser's
// default link colour — roughly #0000EE, about 2:1 contrast against
// github-dark's #0d1117 background and effectively invisible on nine of
// kx's ten palettes. The CVE ID is the primary key of the table this
// feature exists to show.
func TestAnchorsUseThePaletteAccent(t *testing.T) {
	if !strings.Contains(stylesheet, "a { color: var(--accent); }") {
		t.Error("anchors are not coloured through the palette's accent " +
			"custom property, so links fall back to the browser's default " +
			"colour regardless of theme")
	}
}

// The masthead's plain-text "kx" wordmark was replaced with an inline SVG
// logomark so the report carries the same brand mark as kx --help's kxArt
// banner. It must pick up the theme the same way the text wordmark did: via
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

	for _, want := range []string{
		"ghcr.io/jzills/api-gateway:1.8.3",
		"CVE-2021-22945",
		"curl",
		// html/template's text escaper renders "+" as the numeric character
		// reference "&#43;" in every HTML text context, not just this one —
		// it is not conditioned on the value being page data versus a
		// literal in the template. That is what a browser decodes back to
		// "+", so this is the correctly escaped form of
		// "7.74.0-1.3+deb11u2", the version this task's own fixture carries;
		// a literal "+" byte in the output would mean escaping had been
		// bypassed (e.g. with template.HTML), which the brief forbids.
		"7.74.0-1.3&#43;deb11u2",
		"https://scout.docker.com/v/CVE-2021-22945",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("scan page is missing %q", want)
		}
	}
}

// A package with no fix must show a dash, not an empty cell that reads as
// missing data.
func TestRenderScanShowsDashWhenUnfixed(t *testing.T) {
	out, err := RenderScan(scanPage(t))
	if err != nil {
		t.Fatalf("RenderScan returned %v", err)
	}
	if !strings.Contains(string(out), `<td class="dim">—</td>`) {
		t.Error("an unfixed CVE did not render a dash")
	}
}

// rowContaining returns the <details>...</details> block enclosing the
// first occurrence of marker, so an assertion can be scoped to one image's
// row instead of the whole page.
func rowContaining(t *testing.T, html, marker string) string {
	t.Helper()
	at := strings.Index(html, marker)
	if at < 0 {
		t.Fatalf("html does not contain %q", marker)
	}
	start := strings.LastIndex(html[:at], "<details")
	if start < 0 {
		t.Fatalf("no <details> found before %q", marker)
	}
	relEnd := strings.Index(html[at:], "</details>")
	if relEnd < 0 {
		t.Fatalf("no </details> found after %q", marker)
	}
	return html[start : at+relEnd+len("</details>")]
}

// A failed scan keeps its message and must not show zeroes, which would read
// as a clean result.
//
// The zero-count check is load-bearing, not decorative: scanPage's failed
// image (Error set, Counts left nil) would satisfy "the row is labelled" and
// "the message is present" even if a regression made the template render
// counts unconditionally instead of dashing them out on error — reading a
// severity out of a nil Counts map returns Go's int zero value rather than
// panicking, so the row would silently show "0" in every count column. The
// check is anchored to the literal markup a zero count renders as
// (`<span class="num dim">0</span>`), not a bare "0": the inlined stylesheet
// is full of bare "0"s (padding, border widths, opacity), so that would
// never be able to fail on this regression.
//
// The check is scoped to the failed row specifically (rowContaining), not
// the whole page: scanPage's other image has legitimate zero counts of its
// own (MEDIUM and UNSPECIFIED are both 0 for a real, successfully scanned
// image), which correctly render the very same markup and would make a
// whole-page check fail regardless of whether the failed row itself was
// right.
func TestRenderScanKeepsFailureMessages(t *testing.T) {
	out, err := RenderScan(scanPage(t))
	if err != nil {
		t.Fatalf("RenderScan returned %v", err)
	}
	html := string(out)
	if !strings.Contains(html, "manifest unknown") {
		t.Error("the failure message is missing")
	}
	if !strings.Contains(html, "scan failed") {
		t.Error("the failed row is not labelled")
	}
	failedRow := rowContaining(t, html, "manifest unknown")
	if strings.Contains(failedRow, `<span class="num dim">0</span>`) {
		t.Error("a failed scan rendered a zero count instead of a dash")
	}
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
	if strings.Contains(string(out), "<script>alert(1)") || strings.Contains(string(out), "<b>nope</b>") {
		t.Error("unescaped scanner content reached the page")
	}
}

// TestRenderScanEscapesImageNames above mutates the image row that has Error
// set, so the drawer renders the error message and the CVE table
// (scan.gohtml's {{else if .Findings}} branch) is never reached — ID,
// Package, Installed and URL have no escaping coverage anywhere else. URL in
// particular flows into an href attribute, a URL context html/template
// escapes with urlFilter rather than the plain text escaper the other three
// fields go through, so it needs its own case: a dangerous scheme such as
// "javascript:" is not encoded, it is replaced wholesale with the literal
// "#ZgotmplZ".
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

	for _, forbidden := range []string{
		"<script>alert",
		"<b>package</b>",
		"<i>installed</i>",
		"javascript:alert",
	} {
		if strings.Contains(html, forbidden) {
			t.Errorf("unescaped or unneutralised cluster content reached the CVE table: %q", forbidden)
		}
	}

	// Positive assertions: each field must actually have rendered in its
	// escaped form, not simply be absent — a template that stopped emitting
	// a field would satisfy every check above just as well as one that
	// correctly escaped it.
	for _, want := range []string{
		`&lt;script&gt;alert(&#34;id&#34;)&lt;/script&gt;`,
		`&lt;b&gt;package&lt;/b&gt;`,
		`&lt;i&gt;installed&lt;/i&gt;`,
		`href="#ZgotmplZ"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the CVE table is missing %q", want)
		}
	}
}

// style.css's ".row > summary.scan-grid, .sweep-head.scan-grid" rule only
// matches because scan.gohtml puts the "scan-grid" class on <summary>
// itself (paired with "row" only via the ">" combinator to its ancestor
// <details>), not on <details> the way ".ns-grid" pairs with ".row"
// (compare TestRenderDiagAllNamespacesUsesItsOwnGrid, which pins that
// placement for ns-grid). Moving the class from <summary> to <details> —
// the ns-grid shape — would silently stop the CSS selector from matching
// anything, since ".row.scan-grid > summary" (an unstyled summary) is a
// different selector than ".row > summary.scan-grid", and no other test in
// this file checks where the class actually lives, only that the grid
// renders content correctly.
func TestRenderScanPutsGridClassOnHeaderAndSummary(t *testing.T) {
	out, err := RenderScan(scanPage(t))
	if err != nil {
		t.Fatalf("RenderScan returned %v", err)
	}
	html := string(out)
	if !strings.Contains(html, `<div class="sweep-head scan-grid">`) {
		t.Error("the scan header did not get its own grid")
	}
	if !strings.Contains(html, `<summary class="scan-grid">`) {
		t.Error("scan rows did not get their own grid on the <summary> element itself")
	}
}

// scanner.Severities has five buckets, not four: CountBySeverity folds any
// finding ParseFindings could not map to a known severity into UNSPECIFIED,
// deliberately, so it isn't dropped from the total (internal/scanner/scanner.go).
// Before this fix, scan.gohtml only had CRIT/HIGH/MED/LOW columns — an image
// whose findings were all UNSPECIFIED would show every visible count as
// zero, reading as a clean scan while its drawer listed real CVEs
// underneath. render.ScanSummary (internal/render/scan.go) has always had a
// UNSPEC column for exactly this reason, and page.go's own package doc says
// the page and terminal must not disagree about severity.
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
	if !strings.Contains(html, `<span class="num dim">3</span>`) {
		t.Error("an image with only UNSPECIFIED findings did not show its UNSPEC count")
	}
	if !strings.Contains(html, "CVE-2024-00001") {
		t.Error("an image with only UNSPECIFIED findings did not list its CVEs in the drawer")
	}
}

// An image scanned clean gets no severity bands (a zero total is a special
// case in severityBar: without it, dividing by a zero total panics) and the
// drawer says so in words rather than showing an empty table. If the
// template stopped calling severityBar and instead always drew one full
// band, or rendered the findings table unconditionally instead of branching
// on {{if .Findings}}, this would catch it.
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
	if !strings.Contains(html, "No vulnerabilities found") {
		t.Error("a clean image did not render the empty state")
	}
	for _, band := range []string{`<i class="crit"`, `<i class="high"`, `<i class="med"`, `<i class="low"`} {
		if strings.Contains(html, band) {
			t.Errorf("a clean image rendered a %s severity band", band)
		}
	}
}
