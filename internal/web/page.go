// Package web renders kx's analysis output as a themed HTML page and serves it
// from a loopback port.
//
// Rendering and serving are separate on purpose: the Render functions are pure
// functions over the same values the terminal renderer consumes, so a page can
// be tested by comparing bytes without a socket or a browser.
//
// internal/render knows nothing about this package, and this package adds no
// rendering to it — it borrows two classifications so the page and the
// terminal cannot disagree about severity.
package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/mark"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/scanner"
	"github.com/jzills/kx/internal/theme"
	"github.com/jzills/kx/internal/tree"
)

// Meta is the provenance every page carries: what was run, when, and where it
// is being served.
//
// Named Meta rather than Chrome because theme.Chrome is the palette's page
// colours, and two unrelated Chromes one import apart would be a trap.
type Meta struct {
	Title      string
	Invocation string
	Captured   time.Time
	URL        string
	// Styles is theme.WebStyles' output for the active palette.
	Styles map[string]string
}

// DiagPage covers both diagnostic shapes. A single-resource report is a sweep
// of one with Single set: the template renders the same report block either
// way, inline or inside a <details>, which is what stops the two views
// drifting apart.
type DiagPage struct {
	Meta
	// Scope is the namespace, or "all namespaces".
	Scope         string
	AllNamespaces bool
	Single        bool
	Checked       int
	// Reports are every swept resource, most severe first, healthy included —
	// or exactly one resource when Single is set, healthy or not.
	Reports []diagnostics.Report
}

// ScanPage is one image-scan sweep.
type ScanPage struct {
	Meta
	Scope  string
	Images []scanner.ImageScan
}

// TreePage is one ownership-graph rendering, for a single resource, a whole
// namespace sweep, or an -A sweep across every namespace — the same
// *tree.Node(s) the terminal's render.Tree draws, wrapped with page
// provenance.
type TreePage struct {
	Meta
	// Scope is the muted caption line above the tree, e.g. "Namespace · prod",
	// "Deployment/web · prod", or "Namespace · all namespaces" — the same text
	// render.Banner/render.ScopeBanner already printed to the terminal just
	// above render.Tree, so the two must not read differently.
	Scope string
	// AllNamespaces sweeps render one root per namespace, in Roots, rather
	// than the single Root every other shape uses.
	AllNamespaces bool
	Root          *tree.Node
	Roots         []*tree.Node
}

// TopPage is one kx top listing — pods (default) or nodes — rendered as a
// page.
//
// Unlike DiagPage/ScanPage/TreePage, there is no richer domain struct
// behind it (no diagnostics.Report/scanner.ImageScan equivalent): the
// kubectl table text TopCommand already produces is the whole of the data,
// so Rows is built directly from that text (see topPageRows in
// internal/cli) rather than converted here from an intermediate type — the
// way diagRows/scanImageRows/treeRows convert for the other three pages.
type TopPage struct {
	Meta
	Scope string
	Rows  []TopRow
}

// TopRow is one pod's or node's usage.
type TopRow struct {
	Index int
	Name  string
	// Namespace is empty in single-namespace mode (no NAMESPACE column to
	// read it from) — the grid only shows this column when at least one
	// row actually has one, matching the -A-only NAMESPACE column
	// kubectl top pods -A's own terminal output already has. An -A listing
	// carries both this and an Index now.
	Namespace      string
	CPU, Memory    string
	CPUPct, MemPct Usage
}

// Segment is one band of the stacked severity bar.
type Segment struct {
	Class string
	Pct   int
}

// severityBar turns severity counts into the bands of a stacked bar. An image
// with no findings gets no bands rather than a full-width empty one.
func severityBar(counts map[string]int) []Segment {
	total := 0
	for _, severity := range scanner.Severities {
		total += counts[severity]
	}
	if total == 0 {
		return nil
	}
	classes := map[string]string{
		"CRITICAL": "crit", "HIGH": "high", "MEDIUM": "med",
		"LOW": "low", "UNSPECIFIED": "low",
	}
	var bands []Segment
	for _, severity := range scanner.Severities {
		count := counts[severity]
		if count == 0 {
			continue
		}
		bands = append(bands, Segment{
			Class: classes[severity],
			Pct:   count * 100 / total,
		})
	}
	return bands
}

// Usage is a container's consumption of one resource against its limit.
//
// Known is false when no limit is set, which the page draws as an em dash.
// "No limit configured" and "using none of its limit" are different facts and
// must not both render as 0%.
type Usage struct {
	Known bool
	Pct   int
	// Class is the CSS class for the severity of this percentage, or "" when
	// the percentage warrants no styling.
	Class string
}

// usageOf converts a usage/limit pair into a drawable percentage.
//
// Integer arithmetic, multiplying before dividing, matching
// diagnostics.usagePercent and cli.percentCell exactly — the page and the
// terminal must not disagree by a point on the same container.
//
// MilliValue scales by 1000 and the percentage by a further 100, so a byte
// count is held at 100,000×: int64 overflows above roughly 92 TB of usage,
// which no container limit reaches. CPU is exact at milli scale.
func usageOf(used, limit *resource.Quantity, kind string) Usage {
	if used == nil || limit == nil || limit.IsZero() {
		return Usage{}
	}
	pct := int(used.MilliValue() * 100 / limit.MilliValue())
	return NewUsage(pct, kind)
}

// NewUsage builds a page-ready usage percentage from an already-known pct —
// for callers (like kx top's row conversion, which parses percentages out
// of kubectl's own table text) that have a percentage already, rather than
// computing one from a quantity pair the way usageOf does.
func NewUsage(pct int, kind string) Usage {
	return Usage{Known: true, Pct: pct, Class: styleClass(render.UsageStyle(pct, kind))}
}

// styleClass turns a semantic style name into a CSS class ("status.ok" →
// "status-ok"), so the palette's vocabulary reaches the stylesheet unchanged.
// An empty name yields an empty class rather than a stray "-".
func styleClass(name string) string {
	if name == "" {
		return ""
	}
	return strings.ReplaceAll(name, ".", "-")
}

func severityClass(severity diagnostics.Severity) string {
	switch severity {
	case diagnostics.Critical:
		return "status-bad"
	case diagnostics.Warning:
		return "status-warn"
	default:
		return "status-ok"
	}
}

// severityIcon matches the terminal's markers exactly, so a screenshot of one
// reads the same as the other.
func severityIcon(severity diagnostics.Severity) string {
	switch severity {
	case diagnostics.Critical:
		return "✗"
	case diagnostics.Warning:
		return "!"
	default:
		return "✓"
	}
}

// cssVars renders the palette as custom-property declarations.
//
// This is the one place page content becomes template.CSS, and it is safe
// because the values never come from page data: theme.WebStyles returns only
// #rrggbb, guarded by a test over every registered theme. Do not generalise
// from this — nothing else in this package may be marked pre-escaped.
func cssVars(styles map[string]string) template.CSS {
	var out strings.Builder
	out.WriteString(":root{")
	// Sorted so the output is byte-stable for golden-file tests; Go map
	// iteration order is randomised.
	for _, name := range sortedKeys(styles) {
		fmt.Fprintf(&out, "--%s:%s;", styleClass(name), styles[name])
	}
	out.WriteString("}")
	return template.CSS(out.String())
}

// faviconURI draws the mark as the page's tab icon, in the palette's accent,
// as a data: URI rather than a file — a report is one self-contained document,
// served from a loopback port or saved to disk, with nowhere to link a second
// asset from.
//
// template.URL, not a string: html/template rewrites any URL whose scheme
// isn't http, https or mailto to "#ZgotmplZ", which would leave every page
// with a broken icon. Safe here for the same reason cssVars is — the only
// value interpolated is theme.WebStyles' accent, never page data — and base64
// leaves nothing to quote inside the attribute either way.
func faviconURI(styles map[string]string) template.URL {
	accent := styles[theme.Accent]
	if accent == "" {
		// A Meta built without Styles — several tests do — still gets a
		// visible icon, matching how an empty theme name resolves in
		// cli.pageMeta rather than rendering an unfilled black tile.
		if fallback, err := theme.WebStyles(theme.Default); err == nil {
			accent = fallback[theme.Accent]
		}
	}
	svg := mark.Favicon(accent)
	return template.URL("data:image/svg+xml;base64," +
		base64.StdEncoding.EncodeToString([]byte(svg)))
}

func sortedKeys(styles map[string]string) []string {
	names := make([]string, 0, len(styles))
	for name := range styles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DiagRow is one sweep row for the diag grid — a thin, JSON-friendly view
// over diagnostics.Report built by diagRows, the same conversion the sweep
// body renders from, so the grid and the narrative report cannot disagree.
//
// Row is the 0-based index into DiagPage.Reports. It is stable regardless of
// how the grid re-sorts, re-filters, or re-groups, and is how a row-click
// finds its matching <template id="diag-detail-N"> for the expansion panel —
// the full nested report is server-rendered once and cloned client-side
// rather than reimplemented in JS from JSON.
type DiagRow struct {
	Row       int
	Index     int // 1-based, matches the sweep's index column
	Kind      string
	Name      string
	Namespace string
	Verdict   string
	// VerdictRank sorts by severity (OK < Warning < Critical); Verdict's text
	// ("critical" < "healthy" < "warnings") does not sort that way.
	VerdictRank int
	TopFinding  string
}

func diagRows(reports []diagnostics.Report) []DiagRow {
	rows := make([]DiagRow, len(reports))
	for i, r := range reports {
		row := DiagRow{
			Row:         i,
			Index:       i + 1,
			Kind:        string(r.Kind),
			Name:        r.Name,
			Namespace:   r.Namespace,
			Verdict:     r.Verdict.String(),
			VerdictRank: int(r.Verdict),
		}
		if len(r.Findings) > 0 {
			row.TopFinding = r.Findings[0].Summary
		}
		rows[i] = row
	}
	return rows
}

// ScanImageRow is one image-level row for the scan page's image grid, built
// by scanImageRows over scanner.ImageScan. Bar reuses severityBar's own
// bands rather than having the grid's JS re-derive them from Counts, so the
// stacked bar can't drift from the one severityBar already draws elsewhere.
type ScanImageRow struct {
	Image                                    string
	Error                                    string
	Critical, High, Medium, Low, Unspecified int
	Bar                                      []Segment
}

func scanImageRows(images []scanner.ImageScan) []ScanImageRow {
	rows := make([]ScanImageRow, len(images))
	for i, img := range images {
		rows[i] = ScanImageRow{
			Image:       img.Image,
			Error:       img.Error,
			Critical:    img.Counts["CRITICAL"],
			High:        img.Counts["HIGH"],
			Medium:      img.Counts["MEDIUM"],
			Low:         img.Counts["LOW"],
			Unspecified: img.Counts["UNSPECIFIED"],
			Bar:         severityBar(img.Counts),
		}
	}
	return rows
}

// ScanFindingRow is one CVE, flattened across every image so the findings
// grid can search/group across the whole sweep rather than one image's
// <details> at a time.
type ScanFindingRow struct {
	Image     string
	ID        string
	URL       string
	Severity  string
	Package   string
	Installed string
	FixedIn   string
	Fixable   bool
}

func scanFindingRows(images []scanner.ImageScan) []ScanFindingRow {
	var rows []ScanFindingRow
	for _, img := range images {
		for _, f := range img.Findings {
			rows = append(rows, ScanFindingRow{
				Image:     img.Image,
				ID:        f.ID,
				URL:       f.URL,
				Severity:  f.Severity,
				Package:   f.Package,
				Installed: f.Installed,
				FixedIn:   f.FixedIn,
				Fixable:   f.FixedIn != "",
			})
		}
	}
	return rows
}

// TreeRow is a JSON-friendly view over *tree.Node for Tabulator's dataTree
// mode. It is a deliberate copy rather than tree.Node itself: that package is
// shared with the terminal renderer, and its Go-idiomatic Children field
// shouldn't bend to Tabulator's own "_children" wire convention.
type TreeRow struct {
	Label    string
	Style    string
	Index    int
	Children []TreeRow `json:"_children,omitempty"`
}

func treeRows(n *tree.Node) TreeRow {
	if n == nil {
		return TreeRow{}
	}
	row := TreeRow{Label: n.Label, Style: n.Style, Index: n.Index}
	if len(n.Children) > 0 {
		row.Children = make([]TreeRow, len(n.Children))
		for i, c := range n.Children {
			row.Children[i] = treeRows(c)
		}
	}
	return row
}

func treeRootRows(roots []*tree.Node) []TreeRow {
	rows := make([]TreeRow, len(roots))
	for i, r := range roots {
		rows[i] = treeRows(r)
	}
	return rows
}

// marshalJS renders a view type as a JSON literal safe to embed inside a
// <script type="application/json"> block.
//
// json.Marshal's default HTML-escaping (< > & become < > &)
// is what stops a CVE description or log line containing "</script>" from
// breaking out of the block; template.JS marks the result pre-escaped so
// html/template's own script-context escaping doesn't run a second pass over
// already-valid JSON and corrupt it. The view types above never contain
// channels, funcs, or cycles, so Marshal cannot fail here — a failure would
// be a programming error, not a runtime condition to recover from.
func marshalJS(v any) template.JS {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return template.JS(b)
}

// funcs are the derivations templates cannot express.
var funcs = template.FuncMap{
	"cssVars":    cssVars,
	"favicon":    faviconURI,
	"stylesheet": func() template.CSS { return template.CSS(stylesheet) },
	// wordmark is safe to mark pre-escaped for the same reason stylesheet is:
	// it is a compiled-in asset, never page data, guarded by
	// TestWordmarkUsesThePaletteAccent.
	"wordmark":      func() template.HTML { return template.HTML(wordmarkSVG) },
	"statusClass":   func(status string) string { return styleClass(render.StatusStyle(status)) },
	"severityClass": severityClass,
	"severityIcon":  severityIcon,
	// age is a stub so the templates parse; RenderDiag and RenderScan rebind
	// it per call to a closure over the page's own Captured time, so the same
	// page value always renders the same bytes rather than reading the clock.
	"age": func(time.Time) string { return "" },
	"cpuUsage": func(c diagnostics.ContainerDiagnostic) Usage {
		return usageOf(c.CPUUsage, c.CPULimit, "cpu")
	},
	"memoryUsage": func(c diagnostics.ContainerDiagnostic) Usage {
		return usageOf(c.MemoryUsage, c.MemoryLimit, "memory")
	},
	"ready": func(pod diagnostics.PodDiagnostic) string {
		return fmt.Sprintf("%d/%d", pod.ReadyContainers, pod.TotalContainers)
	},
	// reason is whichever of the two the container actually has; the terminal
	// table collapses them into one column the same way.
	"reason": func(c diagnostics.ContainerDiagnostic) string {
		if c.WaitingReason != "" {
			return c.WaitingReason
		}
		return c.TerminatedReason
	},
	// Indexes are 1-based, matching every other kx listing; templates have no
	// arithmetic of their own.
	"add":         func(a, b int) int { return a + b },
	"severityBar": severityBar,
	"severities":  func() []string { return scanner.Severities },
	"count":       func(counts map[string]int, severity string) int { return counts[severity] },

	// Vendored (see vendor.go) and first-party (see render.go) grid assets,
	// inlined the same way stylesheet/wordmark are: pageHandler answers every
	// request with one in-memory document, so there is nothing to reference
	// by URL.
	"tabulatorJS":  func() template.JS { return template.JS(tabulatorJS) },
	"tabulatorCSS": func() template.CSS { return template.CSS(tabulatorCSS) },
	"kxGridCSS":    func() template.CSS { return template.CSS(kxGridCSS) },
	"kxGridJS":     func() template.JS { return template.JS(kxGridJS) },

	"diagRowsJSON":        func(reports []diagnostics.Report) template.JS { return marshalJS(diagRows(reports)) },
	"scanImageRowsJSON":   func(images []scanner.ImageScan) template.JS { return marshalJS(scanImageRows(images)) },
	"scanFindingRowsJSON": func(images []scanner.ImageScan) template.JS { return marshalJS(scanFindingRows(images)) },
	"treeRowsJSON":        func(root *tree.Node) template.JS { return marshalJS([]TreeRow{treeRows(root)}) },
	"treeRootRowsJSON":    func(roots []*tree.Node) template.JS { return marshalJS(treeRootRows(roots)) },
	// Unlike the other *RowsJSON funcs, this is a plain marshal with no
	// private converter behind it — TopRow already is the view type, built
	// in internal/cli (see TopPage's doc comment above).
	"topRowsJSON": func(rows []TopRow) template.JS { return marshalJS(rows) },
}
