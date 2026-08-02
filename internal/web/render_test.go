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

// Carried defect: .row.ns-grid > summary and .row > summary.scan-grid are
// compound selectors (two classes plus a type), giving them higher
// specificity than the plain ".row > summary, .sweep-head" rule inside
// @media (max-width: 720px) — and @media contributes no specificity of its
// own. Unless the mobile rule repeats both modifiers at the same
// specificity, the desktop grid wins the cascade under 720px and neither
// the -A sweep nor the scan page collapses to the narrow layout.
//
// This checks the necessary condition — that the selector list inside the
// media query actually names both modifiers — which is what a fix of this
// kind changes. It cannot check the sufficient one, that the mobile rule
// then wins the cascade in an actual browser at an actual viewport width:
// that is a rendering question no Go string comparison can answer, and
// nothing in this package parses or computes CSS specificity outside this
// test. Reasoning about the specificity arithmetic (above, and in the CSS
// comments beside both rules) is what stands in for that.
func TestMobileCollapseSelectorListCoversGridModifiers(t *testing.T) {
	block := mediaQueryBlock(t, stylesheet, "@media (max-width: 720px)")
	for _, modifier := range []string{".ns-grid", ".scan-grid"} {
		if !strings.Contains(block, modifier) {
			t.Errorf("the mobile collapse rule does not mention %s, so it loses "+
				"the specificity contest against the desktop rule and never "+
				"collapses under 720px", modifier)
		}
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

// A failed scan keeps its message and must not show zeroes, which would read
// as a clean result.
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
