package render

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/kinds"
)

func sampleReport() diagnostics.Report {
	return diagnostics.BuildReport(diagnostics.Data{
		Kind: kinds.Deployment, Name: "api", Namespace: "prod",
		Replicas: &diagnostics.ReplicaHealth{Desired: 1},
		Pods: []diagnostics.PodDiagnostic{{
			Name: "api-abc", Phase: "Pending", TotalContainers: 1,
			Scheduling: diagnostics.SchedulingInfo{Schedulable: true},
			Containers: []diagnostics.ContainerDiagnostic{{
				Name: "api", WaitingReason: "ImagePullBackOff", RestartCount: 2,
				LogLines:  []string{"ERROR pull failed", "retrying"},
				LogSource: "current", LogFiltered: true,
			}},
		}},
		WarningEvents: []diagnostics.EventSummary{{
			Reason: "Failed", Kind: "Pod", Name: "api-abc", Count: 4,
			Message: "Failed to pull image",
		}},
	})
}

// A deliberate deviation from the Python renderer: Rich's summary grid pads
// every finding to the console width, which is 1000 columns when piped, leaving
// long runs of trailing spaces in redirected output. The content is identical;
// only that padding is dropped.
//
// Table rows are excluded — their trailing padding is part of the table layout
// this renderer matches byte-for-byte.
func TestFindingLinesHaveNoTrailingWhitespace(t *testing.T) {
	out := capture(func(r *Renderer) { r.Diagnostic(sampleReport()) })
	for i, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if !strings.HasPrefix(trimmed, "✗") && !strings.HasPrefix(trimmed, "!") &&
			!strings.HasPrefix(trimmed, "✓") {
			continue
		}
		if line != strings.TrimRight(line, " \t") {
			t.Errorf("finding on line %d has trailing whitespace: %q", i+1, line)
		}
	}
}

func TestDiagnosticRendersEverySection(t *testing.T) {
	out := capture(func(r *Renderer) { r.Diagnostic(sampleReport()) })
	for _, want := range []string{
		"Deployment/api · prod",
		"SUMMARY",
		"Image pull failure",
		"POD", "CONTAINER", "ImagePullBackOff",
		"LOGS", "ERROR pull failed",
		"WARNING EVENTS", "Failed ×4", "Failed to pull image",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

// A healthy resource still gets its sections, saying so rather than showing
// nothing.
func TestHealthyDiagnosticSaysSo(t *testing.T) {
	report := diagnostics.BuildReport(diagnostics.Data{
		Kind: kinds.Deployment, Name: "api", Namespace: "prod",
		Replicas: &diagnostics.ReplicaHealth{Desired: 1, Ready: 1, Available: 1, Updated: 1},
	})
	out := capture(func(r *Renderer) { r.Diagnostic(report) })
	for _, want := range []string{"healthy", "No issues detected", "No warning events"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

// A raw tail is labelled as such, so it isn't mistaken for matched errors.
func TestUnfilteredLogsAreLabelled(t *testing.T) {
	report := diagnostics.Report{
		Kind: kinds.Pod, Name: "api-abc", Namespace: "prod",
		Pods: []diagnostics.PodDiagnostic{{
			Name: "api-abc", Phase: "Running", TotalContainers: 1,
			Containers: []diagnostics.ContainerDiagnostic{{
				Name: "api", LogLines: []string{"just some output"}, LogFiltered: false,
			}},
		}},
	}
	out := capture(func(r *Renderer) { r.Diagnostic(report) })
	if !strings.Contains(out, "recent output") {
		t.Errorf("raw tail is not labelled:\n%s", out)
	}
}

func TestTriageAllHealthy(t *testing.T) {
	out := capture(func(r *Renderer) {
		r.Triage(TriageResult{Namespace: "prod", Checked: 5, Healthy: 5})
	})
	if !strings.Contains(out, "5 checked · all healthy") {
		t.Errorf("output = %q", out)
	}
}

func TestTriageEmptyNamespace(t *testing.T) {
	out := capture(func(r *Renderer) { r.Triage(TriageResult{Namespace: "prod"}) })
	if !strings.Contains(out, "0 checked") {
		t.Errorf("output = %q", out)
	}
}

func TestTriageReportsDroppedNameCollisions(t *testing.T) {
	out := capture(func(r *Renderer) {
		r.Triage(TriageResult{
			Namespace: "prod", Checked: 3, Healthy: 1,
			Reports: []diagnostics.Report{{
				Kind: kinds.Pod, Name: "api", Verdict: diagnostics.Critical,
				Findings: []diagnostics.Finding{{Severity: diagnostics.Critical, Summary: "broken"}},
			}},
			Dropped: []string{"Service/api"},
		})
	})
	if !strings.Contains(out, "Service/api shares a name") {
		t.Errorf("dropped row not reported:\n%s", out)
	}
}

// Every indexed listing labels its index column "X". The Python renderer left
// the triage table's blank, alone among them; this is a deliberate divergence,
// so it is pinned rather than left to drift back.
func TestTriageIndexColumnIsLabelled(t *testing.T) {
	out := capture(func(r *Renderer) {
		r.Triage(TriageResult{
			Namespace: "prod", Checked: 2, Healthy: 1,
			Reports: []diagnostics.Report{{
				Kind: kinds.Pod, Name: "api", Verdict: diagnostics.Critical,
				Findings: []diagnostics.Finding{{Severity: diagnostics.Critical, Summary: "broken"}},
			}},
		})
	})
	header := strings.Split(out, "\n")[1]
	if !strings.HasPrefix(strings.TrimLeft(header, " "), "X") {
		t.Errorf("header = %q, want it to start with the X index column", header)
	}
}

// The table header sits directly under the caption, as in every other indexed
// listing. The Python renderer printed a blank line between them, alone among
// them; another deliberate divergence, pinned so it can't drift back.
func TestTriageHasNoBlankLineAfterCaption(t *testing.T) {
	out := capture(func(r *Renderer) {
		r.Triage(TriageResult{
			Namespace: "prod", Checked: 2, Healthy: 1,
			Reports: []diagnostics.Report{{
				Kind: kinds.Pod, Name: "api", Verdict: diagnostics.Critical,
				Findings: []diagnostics.Finding{{Severity: diagnostics.Critical, Summary: "broken"}},
			}},
		})
	})
	lines := strings.Split(out, "\n")
	if strings.TrimSpace(lines[1]) == "" {
		t.Errorf("blank line between caption and header:\n%s", out)
	}
}
