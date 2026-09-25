package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/scanner"
	"github.com/jzills/kx/internal/state"
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
// termination, a failed run.
//
// Since is the other half: when an ongoing signal started, for the findings
// no --since window can hide. A finding carries one or the other, never both,
// so a consumer can tell "this failed at 09:41" from "this has been failing
// since 13 August" without parsing the summary.
type jsonFinding struct {
	Severity string `json:"severity"`
	At       string `json:"at,omitempty"`
	Since    string `json:"since,omitempty"`
	Summary  string `json:"summary"`
}

type jsonReport struct {
	Kind      kinds.Kind `json:"kind"`
	Name      string     `json:"name"`
	Namespace string     `json:"namespace,omitempty"`
	// Index is the number this resource was assigned in state, so a
	// consumer that finds something worth acting on can name it — `kx diag
	// 4` — without a second listing. The same convention kx tree --json
	// already uses for jsonTreeNode.Index: a CLI sweep indexes every resource
	// it saves, so there it is always set, but omitempty because the MCP
	// server's documents leave it out unless kx mcp runs with
	// --write-listings, when a diagnose sweep indexes every row it saves.
	Index int `json:"index,omitempty"`
	// Mark is the name a mark-spent reference carried, for the same reason
	// Index exists: so a consumer can name what was diagnosed. A mark has no
	// position in a listing, so Index is 0 for one and omitted — without this
	// the document said nothing at all about which reference produced it.
	Mark     string        `json:"mark,omitempty"`
	Verdict  string        `json:"verdict"`
	Findings []jsonFinding `json:"findings"`
}

// reportOf converts one analysed resource, keeping the findings in the order
// they were sorted into so the JSON and the terminal cannot disagree about
// which one is the headline. ref is what named the resource: a 1-based
// position, a mark, or the zero Ref when the caller has neither — a sweep
// indexes what it saves, so it always has a position to give.
func reportOf(report diagnostics.Report, ref state.Ref) jsonReport {
	findings := make([]jsonFinding, 0, len(report.Findings))
	for _, finding := range report.Findings {
		findings = append(findings, jsonFinding{
			Severity: finding.Severity.Token(),
			At:       rfc3339(finding.At),
			Since:    rfc3339(finding.Since),
			Summary:  finding.Summary,
		})
	}
	return jsonReport{
		Kind:      report.Kind,
		Name:      report.Name,
		Namespace: report.Namespace,
		Index:     ref.Index,
		Mark:      ref.Mark,
		Verdict:   report.Verdict.Token(),
		Findings:  findings,
	}
}

// windowLabel spells a document's window in the vocabulary --since reads, so
// the value a report names is one that can be typed straight back at it. An
// unbounded run names none.
func windowLabel(window time.Duration) string {
	if window <= 0 {
		return ""
	}
	return config.FormatDuration(window)
}

// rfc3339 formats a timestamp for a document, and an unset one as an absent
// field rather than as year 1.
func rfc3339(timestamp time.Time) string {
	if timestamp.IsZero() {
		return ""
	}
	return timestamp.UTC().Format(time.RFC3339)
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
func diagnosticJSON(report diagnostics.Report, ref state.Ref) (string, error) {
	return encode(diagnosticDocumentOf(report, ref))
}

// diagnosticDocumentOf is diagnosticJSON's document, unencoded, for the MCP
// server to return as structured content.
func diagnosticDocumentOf(report diagnostics.Report, ref state.Ref) diagnosticDocument {
	healthy := 0
	if report.Verdict == diagnostics.OK {
		healthy = 1
	}
	return diagnosticDocument{
		SchemaVersion: reportSchemaVersion,
		Kind:          report.Kind,
		Name:          report.Name,
		Namespace:     report.Namespace,
		Window:        windowLabel(report.Window),
		Checked:       1,
		Healthy:       healthy,
		Resources:     []jsonReport{reportOf(report, ref)},
	}
}

// diagnosticDocument is the one shape kx diag --json emits, indexed or swept.
//
// Kind and Name are the subject an index named, and are absent for a sweep,
// which is about a namespace rather than a resource — the same distinction kx
// scan and kx tree draw between an indexed run and a swept one. The resource
// itself still appears in Resources, so nothing has to read the subject to
// find the findings.
type diagnosticDocument struct {
	SchemaVersion int        `json:"schemaVersion"`
	Kind          kinds.Kind `json:"kind,omitempty"`
	Name          string     `json:"name,omitempty"`
	Namespace     string     `json:"namespace,omitempty"`
	AllNamespaces bool       `json:"allNamespaces,omitempty"`
	// Window is how far back the run was allowed to look, spelled the way
	// --since reads it — "24h", "7d". Absent when the run was unbounded,
	// since "0" would read as a setting rather than as the absence of one.
	//
	// The terminal says this in its banner and the HTML report on its
	// invocation line; a document that omitted it was the one surface that
	// could not, and it is the surface a CI job parses beside the --fail-on
	// gate the same window governs. Two runs of the same command differ
	// otherwise with nothing to say whether the cluster got better or the
	// window got narrower.
	Window    string       `json:"window,omitempty"`
	Checked   int          `json:"checked"`
	Healthy   int          `json:"healthy"`
	Resources []jsonReport `json:"resources"`
}

// triageJSON serialises a sweep.
//
// Every resource swept, healthy ones included, regardless of --full: --full
// governs how much of a table fits on a screen, and nothing is scrolling past
// a machine. The HTML report takes the same view for the same reason.
func triageJSON(result render.TriageResult) (string, error) {
	return encode(triageDocument(result, true))
}

// triageDocument is triageJSON's document. indexed says whether each resource
// carries the position TriageCommand saved it at; an MCP sweep saves nothing,
// so a number there would name a row of whatever listing the user has open.
func triageDocument(result render.TriageResult, indexed bool) diagnosticDocument {
	resources := make([]jsonReport, 0, len(result.All))
	// 1-based position in result.All, matching the index TriageCommand.Execute
	// just saved to state in this same order — so a finding in the document
	// and the number `kx diag <index>` would show it under are one figure.
	for position, report := range result.All {
		ref := state.Ref{}
		if indexed {
			ref.Index = position + 1
		}
		resources = append(resources, reportOf(report, ref))
	}

	// Namespace is already empty for a cluster-wide sweep — TriageCommand.Execute
	// blanks it before Sweep runs, since there is no single namespace the
	// listing came from.
	return diagnosticDocument{
		SchemaVersion: reportSchemaVersion,
		Namespace:     result.Namespace,
		AllNamespaces: result.AllNamespaces,
		Window:        windowLabel(result.Window),
		Checked:       result.Checked,
		Healthy:       result.Healthy,
		Resources:     resources,
	}
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
	// Truncated is how many findings a limit left unlisted — always zero for
	// kx scan --json, which lists every finding, so omitted there.
	Truncated int `json:"truncated,omitempty"`
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

// scanDocument is the one shape kx scan --json emits, and what the MCP
// server's scan tool returns as structured content.
type scanDocument struct {
	SchemaVersion int         `json:"schemaVersion"`
	Kind          kinds.Kind  `json:"kind,omitempty"`
	Name          string      `json:"name,omitempty"`
	Namespace     string      `json:"namespace,omitempty"`
	AllNamespaces bool        `json:"allNamespaces,omitempty"`
	Images        []jsonImage `json:"images"`
}

// scanDocumentOf converts a scan's rows into scanJSON's document, unencoded,
// for the MCP server to return as structured content.
func scanDocumentOf(subject scanSubject, rows []scanner.ImageScan) scanDocument {
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
	return scanDocument{
		SchemaVersion: reportSchemaVersion, Kind: subject.Kind, Name: subject.Name,
		Namespace: subject.Namespace, AllNamespaces: subject.AllNamespaces, Images: images,
	}
}

// scanJSON serialises a scan's rows, the same ones the summary table and the
// HTML report are built from.
func scanJSON(subject scanSubject, rows []scanner.ImageScan) (string, error) {
	return encode(scanDocumentOf(subject, rows))
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

// treeDocument is the one shape kx tree --json emits, indexed or swept — and
// what the MCP server's tree tool returns as structured content.
//
// Always a list of Roots, even for the one-root shapes, so a consumer parses
// every kx tree document the same way. kx scan already takes that view of its
// images.
type treeDocument struct {
	SchemaVersion int            `json:"schemaVersion"`
	Kind          string         `json:"kind,omitempty"`
	Name          string         `json:"name,omitempty"`
	Namespace     string         `json:"namespace,omitempty"`
	AllNamespaces bool           `json:"allNamespaces,omitempty"`
	Roots         []jsonTreeNode `json:"roots"`
}

// treeDocumentOf converts an ownership graph — one root for an indexed
// resource or a single namespace, several for an -A forest — into
// treeJSON's document, unencoded, for the MCP server to return as structured
// content.
func treeDocumentOf(subject scanSubject, roots []*tree.Node) treeDocument {
	converted := make([]jsonTreeNode, 0, len(roots))
	for _, root := range roots {
		if root != nil {
			converted = append(converted, treeNodeOf(root))
		}
	}
	return treeDocument{
		SchemaVersion: reportSchemaVersion,
		Kind:          string(subject.Kind),
		Name:          subject.Name,
		Namespace:     subject.Namespace,
		AllNamespaces: subject.AllNamespaces,
		Roots:         converted,
	}
}

// treeJSON serialises an ownership graph — one root for an indexed resource or
// a single namespace, several for an -A forest.
func treeJSON(subject scanSubject, roots []*tree.Node) (string, error) {
	return encode(treeDocumentOf(subject, roots))
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

// topDocument is the one shape kx top --json emits, and what the MCP server's
// top tool returns as structured content.
//
// Truncated is how many rows a limit left out — always zero for kx top
// --json, which has no limit of its own and so never sets it — but present
// here rather than as a second type, matching how treeDocument carries a
// Truncated only tree's caller ever sets.
type topDocument struct {
	SchemaVersion int          `json:"schemaVersion"`
	Resource      string       `json:"resource"`
	Namespace     string       `json:"namespace,omitempty"`
	AllNamespaces bool         `json:"allNamespaces,omitempty"`
	Rows          []jsonTopRow `json:"rows"`
	Truncated     int          `json:"truncated,omitempty"`
}

// topDocumentOf converts a usage listing's rows into topJSON's document,
// unencoded, for the MCP server to return as structured content.
//
// Resource names what was listed — "pods" or "nodes" — because the two have
// different percentage meanings: a pod's is against its limits, a node's
// against its capacity, and nothing else in the document says which.
func topDocumentOf(subject scanSubject, resource string, rows []web.TopRow) topDocument {
	converted := make([]jsonTopRow, 0, len(rows))
	for _, row := range rows {
		converted = append(converted, jsonTopRow{
			Index: row.Index, Name: row.Name, Namespace: row.Namespace,
			CPU: row.CPU, Memory: row.Memory,
			CPUPct: percentOf(row.CPUPct), MemoryPct: percentOf(row.MemPct),
		})
	}
	return topDocument{
		SchemaVersion: reportSchemaVersion, Resource: resource,
		Namespace: subject.Namespace, AllNamespaces: subject.AllNamespaces,
		Rows: converted,
	}
}

// topJSON serialises a usage listing, built from the same rows the table and
// the HTML page render.
func topJSON(subject scanSubject, resource string, rows []web.TopRow) (string, error) {
	return encode(topDocumentOf(subject, resource, rows))
}

func percentOf(usage web.Usage) *int {
	if !usage.Known {
		return nil
	}
	return &usage.Pct
}
