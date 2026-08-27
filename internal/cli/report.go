package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/scanner"
)

// reportSchemaVersion is the version of the --json shapes below.
//
// Versioned because this is a public surface the moment it ships: something
// will parse it in a pipeline, and a field moving underneath that is worse than
// a field it can check for. A plain int, matching how state.json versions
// itself — there is no partial-compatibility case for semver to express.
const reportSchemaVersion = 1

// jsonFinding is one distilled health signal, as JSON.
//
// Rank is deliberately absent. It orders the findings in the array and the
// array is already in that order, so exposing it would publish an internal
// vocabulary a consumer would have to keep up with for no gain.
type jsonFinding struct {
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
}

type jsonReport struct {
	Kind      kinds.Kind    `json:"kind"`
	Name      string        `json:"name"`
	Namespace string        `json:"namespace,omitempty"`
	Verdict   string        `json:"verdict"`
	Findings  []jsonFinding `json:"findings"`
}

// reportOf converts one analysed resource, keeping the findings in the order
// they were sorted into so the JSON and the terminal cannot disagree about
// which one is the headline.
func reportOf(report diagnostics.Report) jsonReport {
	findings := make([]jsonFinding, 0, len(report.Findings))
	for _, finding := range report.Findings {
		findings = append(findings, jsonFinding{
			Severity: finding.Severity.String(),
			Summary:  finding.Summary,
		})
	}
	return jsonReport{
		Kind:      report.Kind,
		Name:      report.Name,
		Namespace: report.Namespace,
		Verdict:   report.Verdict.String(),
		Findings:  findings,
	}
}

// diagnosticJSON serialises one resource's report.
func diagnosticJSON(report diagnostics.Report) (string, error) {
	return encode(struct {
		SchemaVersion int `json:"schemaVersion"`
		jsonReport
	}{reportSchemaVersion, reportOf(report)})
}

// triageJSON serialises a sweep.
//
// Every resource swept, healthy ones included, regardless of --full: --full
// governs how much of a table fits on a screen, and nothing is scrolling past
// a machine. The HTML report takes the same view for the same reason.
func triageJSON(result render.TriageResult) (string, error) {
	source := result.All
	if len(source) == 0 {
		source = result.Reports
	}
	resources := make([]jsonReport, 0, len(source))
	for _, report := range source {
		resources = append(resources, reportOf(report))
	}

	namespace := result.Namespace
	if result.AllNamespaces {
		namespace = ""
	}
	return encode(struct {
		SchemaVersion int          `json:"schemaVersion"`
		Namespace     string       `json:"namespace,omitempty"`
		AllNamespaces bool         `json:"allNamespaces,omitempty"`
		Checked       int          `json:"checked"`
		Healthy       int          `json:"healthy"`
		Resources     []jsonReport `json:"resources"`
	}{
		reportSchemaVersion, namespace, result.AllNamespaces,
		result.Checked, result.Healthy, resources,
	})
}

type jsonVulnerability struct {
	ID        string `json:"id"`
	Severity  string `json:"severity"`
	Package   string `json:"package,omitempty"`
	Installed string `json:"installed,omitempty"`
	FixedIn   string `json:"fixedIn,omitempty"`
	URL       string `json:"url,omitempty"`
}

type jsonImage struct {
	Image    string              `json:"image"`
	Error    string              `json:"error,omitempty"`
	Counts   map[string]int      `json:"counts,omitempty"`
	Findings []jsonVulnerability `json:"findings"`
}

// scanJSON serialises a scan's rows, the same ones the summary table and the
// HTML report are built from.
func scanJSON(scope string, rows []scanner.ImageScan) (string, error) {
	images := make([]jsonImage, 0, len(rows))
	for _, row := range rows {
		findings := make([]jsonVulnerability, 0, len(row.Findings))
		for _, finding := range row.Findings {
			findings = append(findings, jsonVulnerability{
				ID:        finding.ID,
				Severity:  finding.Severity,
				Package:   finding.Package,
				Installed: finding.Installed,
				FixedIn:   finding.FixedIn,
				URL:       finding.URL,
			})
		}
		images = append(images, jsonImage{
			Image: row.Image, Error: row.Error, Counts: row.Counts, Findings: findings,
		})
	}
	return encode(struct {
		SchemaVersion int         `json:"schemaVersion"`
		Scope         string      `json:"scope"`
		Images        []jsonImage `json:"images"`
	}{reportSchemaVersion, scope, images})
}

// encode renders a document indented, because a human reads this too — a
// pipeline pipes it to jq either way.
func encode(document any) (string, error) {
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// diagnosticThresholds are the verdicts --fail-on accepts.
//
// "warnings" is accepted alongside "warning" because that is what a verdict
// prints as — on screen and in --json — so anyone who read one and typed it
// back would otherwise be told it is invalid. The plural reads correctly as a
// verdict ("Deployment/api · warnings") and wrongly as a threshold ("fail on
// warnings or worse"), which is why both spellings exist rather than one.
var diagnosticThresholds = map[string]diagnostics.Severity{
	"warning":  diagnostics.Warning,
	"warnings": diagnostics.Warning,
	"critical": diagnostics.Critical,
}

// parseDiagnosticThreshold reads a --fail-on value for kx diag.
//
// "healthy" is not accepted: it would fail on every run, which is not a gate
// but a broken pipeline.
func parseDiagnosticThreshold(value string) (diagnostics.Severity, error) {
	severity, ok := diagnosticThresholds[strings.ToLower(value)]
	if !ok {
		return 0, fmt.Errorf(
			"Invalid value for '--fail-on': '%s'. Accepted values: critical, warning.", value)
	}
	return severity, nil
}

// scanThresholdBreached reports whether any image carries a vulnerability at or
// above a severity.
//
// An image whose scan failed breaches every threshold. A gate exists to answer
// "is this safe to ship", and an image kx could not read has not been shown to
// be — passing it would let an unreachable registry quietly turn the check off.
func scanThresholdBreached(rows []scanner.ImageScan, value string) (bool, error) {
	wanted := strings.ToUpper(value)
	cutoff := -1
	for position, severity := range scanner.Severities {
		if severity == wanted {
			cutoff = position
			break
		}
	}
	// Severities is ordered most severe first, and UNSPECIFIED is a bucket
	// rather than a level — "fail on unspecified or worse" means nothing.
	if cutoff < 0 || wanted == "UNSPECIFIED" {
		return false, fmt.Errorf(
			"Invalid value for '--fail-on': '%s'. Accepted values: critical, high, medium, low.",
			value)
	}

	for _, row := range rows {
		if row.Error != "" {
			return true, nil
		}
		for position := 0; position <= cutoff; position++ {
			if row.Counts[scanner.Severities[position]] > 0 {
				return true, nil
			}
		}
	}
	return false, nil
}
