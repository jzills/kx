package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/scanner"
)

func decodeJSON(t *testing.T, document string) map[string]any {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal([]byte(document), &object); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, document)
	}
	return object
}

// The JSON is a public surface, so it carries a version the way kx's other
// on-disk shape does.
func TestDiagnosticJSONCarriesASchemaVersion(t *testing.T) {
	out, err := diagnosticJSON(diagnostics.Report{
		Kind: kinds.Pod, Name: "web", Namespace: "prod", Verdict: diagnostics.OK,
	})
	if err != nil {
		t.Fatalf("diagnosticJSON: %v", err)
	}
	// A literal, not reportSchemaVersion: comparing the constant against
	// itself passes whatever it is changed to, which is no assertion at all.
	// This is the version this shape shipped as; changing it should mean
	// deliberately changing this line too.
	if got := decodeJSON(t, out)["schemaVersion"]; got != float64(1) {
		t.Errorf("schemaVersion = %v, want 1", got)
	}
}

// The findings in the JSON are the findings on screen, in the same order, or
// the two views disagree about what is wrong.
func TestDiagnosticJSONCarriesTheFindingsInOrder(t *testing.T) {
	report := diagnostics.BuildReport(diagnostics.Data{
		Kind: kinds.Deployment, Name: "api", Namespace: "prod",
		Replicas: &diagnostics.ReplicaHealth{Desired: 1},
		Pods: []diagnostics.PodDiagnostic{{
			Name: "api-abc", Phase: "Running", TotalContainers: 1,
			Containers: []diagnostics.ContainerDiagnostic{
				{Name: "api", WaitingReason: "CrashLoopBackOff", RestartCount: 3},
			},
		}},
	})
	out, err := diagnosticJSON(report)
	if err != nil {
		t.Fatalf("diagnosticJSON: %v", err)
	}

	var decoded struct {
		Verdict  string `json:"verdict"`
		Findings []struct {
			Severity string `json:"severity"`
			Summary  string `json:"summary"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Verdict != "critical" {
		t.Errorf("verdict = %q, want critical", decoded.Verdict)
	}
	if len(decoded.Findings) != len(report.Findings) {
		t.Fatalf("findings = %d, want %d", len(decoded.Findings), len(report.Findings))
	}
	for i, finding := range decoded.Findings {
		if finding.Summary != report.Findings[i].Summary {
			t.Errorf("findings[%d] = %q, want %q", i, finding.Summary, report.Findings[i].Summary)
		}
	}
	if decoded.Findings[0].Severity != "critical" {
		t.Errorf("findings[0].severity = %q, want critical", decoded.Findings[0].Severity)
	}
}

// Every swept resource, healthy ones included — not just the rows the terminal
// table printed. --full governs what fits on a screen, and a machine is not
// scrolling. Reports here holds only the unhealthy row, as it does without
// --full, so a serialiser reading Reports instead of All loses the healthy one.
func TestTriageJSONCarriesEveryResourceNotJustThePrintedRows(t *testing.T) {
	broken := diagnostics.Report{
		Kind: kinds.Deployment, Name: "api", Namespace: "prod",
		Verdict:  diagnostics.Critical,
		Findings: []diagnostics.Finding{{Severity: diagnostics.Critical, Summary: "down"}},
	}
	healthy := diagnostics.Report{
		Kind: kinds.Deployment, Name: "web", Namespace: "prod",
		Verdict: diagnostics.OK,
	}
	result := render.TriageResult{
		Namespace: "prod", Checked: 2, Healthy: 1,
		Reports: []diagnostics.Report{broken},
		All:     []diagnostics.Report{broken, healthy},
	}
	out, err := triageJSON(result)
	if err != nil {
		t.Fatalf("triageJSON: %v", err)
	}
	object := decodeJSON(t, out)
	if object["namespace"] != "prod" {
		t.Errorf("namespace = %v, want prod", object["namespace"])
	}
	if object["checked"] != float64(2) {
		t.Errorf("checked = %v, want 2", object["checked"])
	}
	resources, ok := object["resources"].([]any)
	if !ok || len(resources) != 2 {
		t.Fatalf("resources = %v, want both the broken and the healthy one", object["resources"])
	}
	if !strings.Contains(out, `"name": "web"`) {
		t.Errorf("the healthy resource is missing:\n%s", out)
	}
}

func TestScanJSONCarriesCountsAndFindings(t *testing.T) {
	out, err := scanJSON("prod", []scanner.ImageScan{{
		Image:  "nginx:1.27",
		Counts: map[string]int{"CRITICAL": 1, "HIGH": 2},
		Findings: []scanner.Finding{
			{ID: "CVE-1", Severity: "CRITICAL", Package: "openssl", Installed: "3.0", FixedIn: "3.1"},
		},
	}, {Image: "broken:v1", Error: "pull failed"}})
	if err != nil {
		t.Fatalf("scanJSON: %v", err)
	}

	var decoded struct {
		Images []struct {
			Image    string         `json:"image"`
			Error    string         `json:"error,omitempty"`
			Counts   map[string]int `json:"counts"`
			Findings []struct {
				ID       string `json:"id"`
				Severity string `json:"severity"`
			} `json:"findings"`
		} `json:"images"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded.Images) != 2 {
		t.Fatalf("images = %d, want 2", len(decoded.Images))
	}
	if decoded.Images[0].Counts["CRITICAL"] != 1 {
		t.Errorf("counts = %v, want CRITICAL 1", decoded.Images[0].Counts)
	}
	if len(decoded.Images[0].Findings) != 1 || decoded.Images[0].Findings[0].ID != "CVE-1" {
		t.Errorf("findings = %v, want CVE-1", decoded.Images[0].Findings)
	}
	if decoded.Images[1].Error != "pull failed" {
		t.Errorf("images[1].error = %q, want the scan failure", decoded.Images[1].Error)
	}
}

// --fail-on turns a report into a gate. The threshold is inclusive: --fail-on
// warning fails on a warning and on anything worse.
func TestDiagnosticThreshold(t *testing.T) {
	for _, testCase := range []struct {
		threshold string
		verdict   diagnostics.Severity
		want      bool
	}{
		{"critical", diagnostics.Critical, true},
		{"critical", diagnostics.Warning, false},
		{"critical", diagnostics.OK, false},
		{"warning", diagnostics.Critical, true},
		{"warning", diagnostics.Warning, true},
		{"warning", diagnostics.OK, false},
	} {
		threshold, err := parseDiagnosticThreshold(testCase.threshold)
		if err != nil {
			t.Fatalf("parseDiagnosticThreshold(%q): %v", testCase.threshold, err)
		}
		if got := testCase.verdict >= threshold; got != testCase.want {
			t.Errorf("%s vs --fail-on %s = %v, want %v",
				testCase.verdict, testCase.threshold, got, testCase.want)
		}
	}
}

func TestDiagnosticThresholdRejectsAnUnknownSeverity(t *testing.T) {
	_, err := parseDiagnosticThreshold("catastrophic")
	if err == nil {
		t.Fatal("parseDiagnosticThreshold accepted an unknown severity")
	}
	if !strings.Contains(err.Error(), "critical") {
		t.Errorf("err = %q, want it to name the accepted values", err)
	}
}

// A scan threshold counts findings at or above a severity across every image.
func TestScanThreshold(t *testing.T) {
	rows := []scanner.ImageScan{
		{Image: "a", Counts: map[string]int{"HIGH": 3, "LOW": 9}},
		{Image: "b", Counts: map[string]int{"MEDIUM": 1}},
	}
	for _, testCase := range []struct {
		threshold string
		want      bool
	}{
		{"critical", false},
		{"high", true},
		{"medium", true},
		{"low", true},
	} {
		breached, err := scanThresholdBreached(rows, testCase.threshold)
		if err != nil {
			t.Fatalf("scanThresholdBreached(%q): %v", testCase.threshold, err)
		}
		if breached != testCase.want {
			t.Errorf("--fail-on %s = %v, want %v", testCase.threshold, breached, testCase.want)
		}
	}
}

// An image whose scan failed has no counts to compare. It must not silently
// pass a gate: a CI job that cannot scan an image has not proved it clean.
func TestScanThresholdFailsOnAnUnscannableImage(t *testing.T) {
	rows := []scanner.ImageScan{{Image: "broken:v1", Error: "pull failed"}}
	breached, err := scanThresholdBreached(rows, "critical")
	if err != nil {
		t.Fatalf("scanThresholdBreached: %v", err)
	}
	if !breached {
		t.Error("an image that could not be scanned passed the gate")
	}
}

func TestScanThresholdRejectsAnUnknownSeverity(t *testing.T) {
	if _, err := scanThresholdBreached(nil, "spicy"); err == nil {
		t.Fatal("scanThresholdBreached accepted an unknown severity")
	}
}

// A verdict prints as "warnings", on screen and in --json. Someone who reads
// one and types it back at --fail-on must not be told it is invalid.
func TestDiagnosticThresholdAcceptsTheVerdictSpelling(t *testing.T) {
	printed := diagnostics.Warning.String()
	threshold, err := parseDiagnosticThreshold(printed)
	if err != nil {
		t.Fatalf("parseDiagnosticThreshold(%q): %v — that is how a verdict prints", printed, err)
	}
	if threshold != diagnostics.Warning {
		t.Errorf("threshold = %v, want Warning", threshold)
	}
}
