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
