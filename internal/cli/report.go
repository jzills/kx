package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/scanner"
	"github.com/jzills/kx/internal/tree"
	"github.com/jzills/kx/internal/web"
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
// At is when the reported thing happened, RFC 3339, and is present only for
// the findings that have a moment: a warning event, a container's last
// termination, a failed run. A finding about present state has none, and its
// absence is the signal that no --since window can hide that line.
type jsonFinding struct {
	Severity string `json:"severity"`
	At       string `json:"at,omitempty"`
	Summary  string `json:"summary"`
}

type jsonReport struct {
	Kind      kinds.Kind `json:"kind"`
	Name      string     `json:"name"`
	Namespace string     `json:"namespace,omitempty"`
	// Index is the number this resource was assigned in state, so a
	// consumer that finds something worth acting on can name it — `kx diag
	// 4` — without a second listing. The same convention kx tree --json
	// already uses for jsonTreeNode.Index: present because a sweep indexes
	// every resource it saves, so it is never the zero value in practice,
	// but omitempty rather than required in case a future caller builds one
	// from something that isn't state-backed.
	Index    int           `json:"index,omitempty"`
	Verdict  string        `json:"verdict"`
	Findings []jsonFinding `json:"findings"`
}

// reportOf converts one analysed resource, keeping the findings in the order
// they were sorted into so the JSON and the terminal cannot disagree about
// which one is the headline. index is the 1-based position state.Save
// assigned it, or 0 when the caller has none to give.
func reportOf(report diagnostics.Report, index int) jsonReport {
	findings := make([]jsonFinding, 0, len(report.Findings))
	for _, finding := range report.Findings {
		at := ""
		if !finding.At.IsZero() {
			at = finding.At.UTC().Format(time.RFC3339)
		}
		findings = append(findings, jsonFinding{
			Severity: finding.Severity.Token(),
			At:       at,
			Summary:  finding.Summary,
		})
	}
	return jsonReport{
		Kind:      report.Kind,
		Name:      report.Name,
		Namespace: report.Namespace,
		Index:     index,
		Verdict:   report.Verdict.Token(),
		Findings:  findings,
	}
}

// diagnosticJSON serialises one resource's report. index is the one the
// caller resolved it from, so the document names the same number `kx diag
// <index>` was just run with.
//
// The same shape a sweep produces, with one entry in it. kx diag used to emit
// the report bare at the top level here and a resources list for a sweep, so a
// consumer had to branch on which one it was looking at — while kx scan, kx
// tree and kx top each have one shape whatever they were pointed at. An
// indexed run is a sweep of one, and saying so costs a wrapper and buys a
// pipeline that reads `.resources[]` for both.
func diagnosticJSON(report diagnostics.Report, index int) (string, error) {
	healthy := 0
	if report.Verdict == diagnostics.OK {
		healthy = 1
	}
	return encode(diagnosticDocument{
		SchemaVersion: reportSchemaVersion,
		Kind:          report.Kind,
		Name:          report.Name,
		Namespace:     report.Namespace,
		Checked:       1,
		Healthy:       healthy,
		Resources:     []jsonReport{reportOf(report, index)},
	})
}

// diagnosticDocument is the one shape kx diag --json emits, indexed or swept.
//
// Kind and Name are the subject an index named, and are absent for a sweep,
// which is about a namespace rather than a resource — the same distinction kx
// scan and kx tree draw between an indexed run and a swept one. The resource
// itself still appears in Resources, so nothing has to read the subject to
// find the findings.
type diagnosticDocument struct {
	SchemaVersion int          `json:"schemaVersion"`
	Kind          kinds.Kind   `json:"kind,omitempty"`
	Name          string       `json:"name,omitempty"`
	Namespace     string       `json:"namespace,omitempty"`
	AllNamespaces bool         `json:"allNamespaces,omitempty"`
	Checked       int          `json:"checked"`
	Healthy       int          `json:"healthy"`
	Resources     []jsonReport `json:"resources"`
}

// triageJSON serialises a sweep.
//
// Every resource swept, healthy ones included, regardless of --full: --full
// governs how much of a table fits on a screen, and nothing is scrolling past
// a machine. The HTML report takes the same view for the same reason.
func triageJSON(result render.TriageResult) (string, error) {
	resources := make([]jsonReport, 0, len(result.All))
	// 1-based position in result.All, matching the index TriageCommand.Execute
	// just saved to state in this same order — so a finding in the document
	// and the number `kx diag <index>` would show it under are one figure.
	for position, report := range result.All {
		resources = append(resources, reportOf(report, position+1))
	}

	// Namespace is already empty for a cluster-wide sweep — TriageCommand.Execute
	// blanks it before Sweep runs, since there is no single namespace the
	// listing came from.
	return encode(diagnosticDocument{
		SchemaVersion: reportSchemaVersion,
		Namespace:     result.Namespace,
		AllNamespaces: result.AllNamespaces,
		Checked:       result.Checked,
		Healthy:       result.Healthy,
		Resources:     resources,
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

// scanSubject names what a scan covered: one indexed workload, or a sweep of
// one namespace, or a sweep of every namespace.
//
// A struct rather than the display string this used to be. "Deployment/api"
// made a consumer split a sentence to recover two fields kx already had, a
// sweep's subject was a bare namespace in the same field, and -A spelled
// itself as the literal words "all namespaces" — which no consumer can tell
// from a namespace actually called that. kx diag names its subject with
// fields; kx scan now names it with the same ones.
type scanSubject struct {
	Kind          kinds.Kind
	Name          string
	Namespace     string
	AllNamespaces bool
}

// scanJSON serialises a scan's rows, the same ones the summary table and the
// HTML report are built from.
func scanJSON(subject scanSubject, rows []scanner.ImageScan) (string, error) {
	images := make([]jsonImage, 0, len(rows))
	for _, row := range rows {
		findings := make([]jsonVulnerability, 0, len(row.Findings))
		for _, finding := range row.Findings {
			findings = append(findings, jsonVulnerability{
				ID:        finding.ID,
				Severity:  severityToken(finding.Severity),
				Package:   finding.Package,
				Installed: finding.Installed,
				FixedIn:   finding.FixedIn,
				URL:       finding.URL,
			})
		}
		images = append(images, jsonImage{
			Image: row.Image, Error: row.Error,
			Counts: countTokens(row.Counts), Findings: findings,
		})
	}
	return encode(struct {
		SchemaVersion int         `json:"schemaVersion"`
		Kind          kinds.Kind  `json:"kind,omitempty"`
		Name          string      `json:"name,omitempty"`
		Namespace     string      `json:"namespace,omitempty"`
		AllNamespaces bool        `json:"allNamespaces,omitempty"`
		Images        []jsonImage `json:"images"`
	}{
		reportSchemaVersion, subject.Kind, subject.Name,
		subject.Namespace, subject.AllNamespaces, images,
	})
}

// severityToken is the document spelling of a scanner's severity label.
//
// Lowercased here rather than in the scanner: scanner.Severities are the SARIF
// labels Scout, Trivy and Grype all emit, and the terminal table and the HTML
// report render them as they arrive. A document kx writes uses kx's own
// spelling — the one --fail-on takes, and the one kx diag's verdicts already
// use — so a severity read out of the JSON can be typed straight back at the
// gate.
func severityToken(severity string) string { return strings.ToLower(severity) }

// countTokens re-keys a severity tally into the document's spelling. A nil
// tally — an image whose scan failed — stays nil, so the field is omitted
// rather than serialised as an empty object beside the error that explains it.
func countTokens(counts map[string]int) map[string]int {
	if counts == nil {
		return nil
	}
	tokens := make(map[string]int, len(counts))
	for severity, count := range counts {
		tokens[severityToken(severity)] = count
	}
	return tokens
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
// prints as on screen, so anyone who read one and typed it back would
// otherwise be told it is invalid. The plural reads correctly as a verdict
// ("Deployment/api · warnings") and wrongly as a threshold ("fail on warnings
// or worse"), which is why both spellings exist rather than one. The document
// uses the singular — see Severity.Token.
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

// jsonTreeNode is one node of an ownership graph.
//
// Kind and Name rather than the Label the terminal draws, for the same reason
// kx scan's subject stopped being a display string: a consumer must not have
// to split "rs/web-7d8f" back apart to recover two fields the graph walk
// already had. Index is the number kx tree printed, so a document and a
// terminal agree about which row `kx logs 4` acts on; it is absent for an
// unindexed walk (--no-index) and for containers, which take no index.
//
// A container carries a name and no kind, because it is part of a pod rather
// than a resource of its own.
type jsonTreeNode struct {
	Kind     string         `json:"kind,omitempty"`
	Name     string         `json:"name"`
	Index    int            `json:"index,omitempty"`
	Children []jsonTreeNode `json:"children,omitempty"`
}

func treeNodeOf(node *tree.Node) jsonTreeNode {
	converted := jsonTreeNode{Kind: node.Kind, Name: node.Name, Index: node.Index}
	for _, child := range node.Children {
		converted.Children = append(converted.Children, treeNodeOf(child))
	}
	return converted
}

// treeJSON serialises an ownership graph — one root for an indexed resource or
// a single namespace, several for an -A forest.
//
// Always a list, even for the one-root shapes, so a consumer parses every kx
// tree document the same way. kx scan already takes that view of its images.
func treeJSON(subject scanSubject, roots []*tree.Node) (string, error) {
	converted := make([]jsonTreeNode, 0, len(roots))
	for _, root := range roots {
		if root != nil {
			converted = append(converted, treeNodeOf(root))
		}
	}
	return encode(struct {
		SchemaVersion int            `json:"schemaVersion"`
		Kind          string         `json:"kind,omitempty"`
		Name          string         `json:"name,omitempty"`
		Namespace     string         `json:"namespace,omitempty"`
		AllNamespaces bool           `json:"allNamespaces,omitempty"`
		Roots         []jsonTreeNode `json:"roots"`
	}{
		reportSchemaVersion, string(subject.Kind), subject.Name,
		subject.Namespace, subject.AllNamespaces, converted,
	})
}

// jsonTopRow is one pod's or node's usage.
//
// The percentages are numbers, not the "12%" cells the table prints, and a
// pointer so "not known" is null rather than zero — a pod with no limit set
// has no percentage, and reporting that as 0% would read as idle.
type jsonTopRow struct {
	Index     int    `json:"index,omitempty"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	CPU       string `json:"cpu"`
	Memory    string `json:"memory"`
	CPUPct    *int   `json:"cpuPercent"`
	MemoryPct *int   `json:"memoryPercent"`
}

// topJSON serialises a usage listing, built from the same rows the table and
// the HTML page render.
//
// Resource names what was listed — "pods" or "nodes" — because the two have
// different percentage meanings: a pod's is against its limits, a node's
// against its capacity, and nothing else in the document says which.
func topJSON(subject scanSubject, resource string, rows []web.TopRow) (string, error) {
	converted := make([]jsonTopRow, 0, len(rows))
	for _, row := range rows {
		converted = append(converted, jsonTopRow{
			Index: row.Index, Name: row.Name, Namespace: row.Namespace,
			CPU: row.CPU, Memory: row.Memory,
			CPUPct: percentOf(row.CPUPct), MemoryPct: percentOf(row.MemPct),
		})
	}
	return encode(struct {
		SchemaVersion int          `json:"schemaVersion"`
		Resource      string       `json:"resource"`
		Namespace     string       `json:"namespace,omitempty"`
		AllNamespaces bool         `json:"allNamespaces,omitempty"`
		Rows          []jsonTopRow `json:"rows"`
	}{
		reportSchemaVersion, resource,
		subject.Namespace, subject.AllNamespaces, converted,
	})
}

func percentOf(usage web.Usage) *int {
	if !usage.Known {
		return nil
	}
	return &usage.Pct
}
