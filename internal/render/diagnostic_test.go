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

// --full's Reports already includes healthy resources, so the footer must
// not also claim some were left out — only the index/all-namespaces hint
// survives.
func TestTriageFullOmitsNotShownFooter(t *testing.T) {
	out := capture(func(r *Renderer) {
		r.Triage(TriageResult{
			Namespace: "prod", Checked: 2, Healthy: 1, Full: true,
			Reports: []diagnostics.Report{
				{Kind: kinds.Pod, Name: "api", Verdict: diagnostics.Critical,
					Findings: []diagnostics.Finding{{Severity: diagnostics.Critical, Summary: "broken"}}},
				{Kind: kinds.Pod, Name: "quiet", Verdict: diagnostics.OK},
			},
		})
	})
	if strings.Contains(out, "not shown") {
		t.Errorf("--full footer still claims resources are not shown:\n%s", out)
	}
	if !strings.Contains(out, "kx diag <index> for detail") {
		t.Errorf("--full footer dropped the index hint:\n%s", out)
	}
}

// --full shows everything, so its footer must not also claim a healthy count is
// hidden — cluster-wide sweeps included, which now carry the same index hint as
// every other sweep.
func TestTriageFullAllNamespacesClaimsNothingIsHidden(t *testing.T) {
	out := capture(func(r *Renderer) {
		r.Triage(TriageResult{
			AllNamespaces: true, Checked: 1, Full: true,
			Reports: []diagnostics.Report{
				{Kind: kinds.Pod, Name: "quiet", Namespace: "prod", Verdict: diagnostics.OK},
			},
		})
	})
	if strings.Contains(out, "not shown") {
		t.Errorf("--full footer still claims resources are not shown:\n%s", out)
	}
	if !strings.Contains(out, "kx diag <index> for detail") {
		t.Errorf("--full footer dropped the index hint:\n%s", out)
	}
}

func TestTriageEmptyNamespace(t *testing.T) {
	out := capture(func(r *Renderer) { r.Triage(TriageResult{Namespace: "prod"}) })
	if !strings.Contains(out, "0 checked") {
		t.Errorf("output = %q", out)
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

// A cluster-wide sweep leads with its index, then the namespace: the number is
// what `kx diag <n>` acts on, and the namespace is what separates two rows
// sharing a name. It used to trade the index column away for the namespace,
// back when -A saved no state to index against.
func TestTriageAllNamespacesLeadsWithIndexThenNamespace(t *testing.T) {
	out := capture(func(r *Renderer) {
		r.Triage(TriageResult{
			AllNamespaces: true, Checked: 3, Healthy: 1,
			Reports: []diagnostics.Report{{
				Kind: kinds.Pod, Name: "api", Namespace: "prod",
				Verdict:  diagnostics.Critical,
				Findings: []diagnostics.Finding{{Severity: diagnostics.Critical, Summary: "broken"}},
			}},
		})
	})
	lines := strings.Split(out, "\n")

	if !strings.Contains(lines[0], "all namespaces") {
		t.Errorf("caption = %q, want it scoped to all namespaces", lines[0])
	}
	header := strings.Fields(lines[1])
	if len(header) < 3 || header[0] != "X" || header[1] != "KIND" || header[2] != "NAMESPACE" {
		t.Errorf("header = %q, want X, KIND then NAMESPACE", lines[1])
	}
	row := strings.Fields(lines[2])
	if len(row) < 3 || row[0] != "1" || row[2] != "prod" {
		t.Errorf("row = %q, want index 1 and namespace prod", lines[2])
	}
	if !strings.Contains(out, "kx diag <index> for detail") {
		t.Errorf("footer does not offer the indexes it saved:\n%s", out)
	}
}

// NAMESPACE replaces the index column rather than joining it, so a name is
// charged for one scope column, not two. Charging for both truncates names that
// had room — the terminal is never as wide as the test harness's 1000 columns.
func TestTriageNameBudgetChargesForOneScopeColumn(t *testing.T) {
	const terminal = 120
	reports := []diagnostics.Report{{Namespace: "prod"}}

	indexed := triageNameBudget(terminal, TriageResult{})
	all := triageNameBudget(terminal, TriageResult{AllNamespaces: true, Reports: reports})

	if want := indexed + triageIndexWidth - width("NAMESPACE"); all != want {
		t.Errorf("all-namespaces budget = %d, want %d — the dropped index column"+
			" should return its width to the name", all, want)
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

// A cluster-wide sweep is indexed now, so it needs both columns: the index to
// act on and the namespace to tell two same-named rows apart. It used to swap
// one for the other, because there were no indexes to print.
func TestTriageAllNamespacesKeepsBothIndexAndNamespaceColumns(t *testing.T) {
	out := capture(func(r *Renderer) {
		r.Triage(TriageResult{
			AllNamespaces: true,
			Checked:       2,
			Full:          true,
			Reports: []diagnostics.Report{
				{Kind: kinds.Deployment, Name: "web", Namespace: "prod",
					Verdict: diagnostics.Critical},
				{Kind: kinds.Deployment, Name: "web", Namespace: "staging",
					Verdict: diagnostics.Critical},
			},
		})
	})

	for _, want := range []string{"X", "NAMESPACE", "prod", "staging"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want it to contain %q", out, want)
		}
	}
	// Both rows are named web; without their numbers neither can be acted on.
	for _, want := range []string{"1", "2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want index %q", out, want)
		}
	}
}

// The footer pointed at "indexes not saved for all-namespace listings"; a swept
// -A listing is indexed, so it gets the same hint every other sweep does.
func TestTriageAllNamespacesHintsAtTheIndex(t *testing.T) {
	out := capture(func(r *Renderer) {
		r.Triage(TriageResult{
			AllNamespaces: true, Checked: 1, Full: true,
			Reports: []diagnostics.Report{
				{Kind: kinds.Deployment, Name: "web", Namespace: "prod",
					Verdict: diagnostics.Critical},
			},
		})
	})

	if !strings.Contains(out, "kx diag <index> for detail") {
		t.Errorf("output = %q, want the index hint", out)
	}
}
