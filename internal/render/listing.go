package render

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/scanner"
	"github.com/jzills/kx/internal/theme"
)

// AllNamespaces is how kx names a listing that spans them, wherever a single
// namespace would otherwise be shown: the caption on `kx get -A`, the sweep
// banner, an HTML page's scope, and the state views.
//
// One spelling, because these all answer the same question and a reader moving
// between them should not have to work out whether "all namespaces" and
// "All Namespaces" mean the same scope. Its wording is pinned by a test rather
// than only by the callers that print it.
const AllNamespaces = "all namespaces"

// Pod and workload phases worth coloring. Anything unlisted renders neutral
// rather than guessing, so an unfamiliar status is never miscolored as healthy.
var (
	statusGreen = map[string]bool{
		"Running": true, "Active": true, "Bound": true, "Available": true,
		"Healthy": true, "Completed": true, "Succeeded": true,
	}
	statusYellow = map[string]bool{
		"Pending": true, "Terminating": true, "Unknown": true,
	}
	statusRed = map[string]bool{
		"Error": true, "CrashLoopBackOff": true, "OOMKilled": true, "Failed": true,
		"Evicted": true, "ImagePullBackOff": true, "ErrImagePull": true,
		"InvalidImageName": true,
	}
)

// statusColor maps a STATUS cell onto a semantic style.
func statusColor(status string) string {
	switch {
	case statusGreen[status]:
		return theme.StatusOK
	case statusRed[status]:
		return theme.StatusBad
	case statusYellow[status] || strings.Contains(status, "Init") || status == "ContainerCreating":
		return theme.StatusWarn
	default:
		return theme.StatusNeutral
	}
}

// StatusStyle classifies a phase, state or reason string onto a semantic style
// name. Exported so the HTML renderer classifies identically — a second copy
// of this mapping is exactly the drift the web package exists to avoid.
func StatusStyle(status string) string { return statusColor(status) }

// kx top's usage-percentage thresholds. These mirror the diagnostic thresholds
// (_MEMORY_WARN_THRESHOLD 0.75, _MEMORY_CRITICAL_THRESHOLD 0.90,
// _CPU_WARN_THRESHOLD 0.90) re-expressed as plain percentages: a red 94% in
// kx top means what a critical finding means in kx diag. Kept in sync by hand,
// since render is a lower-level package than the commands.
const (
	memWarnPct     = 75
	memCriticalPct = 90
	cpuWarnPct     = 90 // CPU never reaches critical: throttling, not a crash.
)

// UsageStyle classifies a usage percentage for "memory" or "cpu", returning ""
// when the percentage warrants no styling.
//
// Exported for the same reason as StatusStyle, and more urgently: these
// thresholds are already kept in sync by hand with the diagnostic ones, so a
// third copy would be one copy too many.
func UsageStyle(pct int, resource string) string {
	if resource == "memory" {
		switch {
		case pct >= memCriticalPct:
			return theme.StatusBad
		case pct >= memWarnPct:
			return theme.StatusWarn
		}
		return ""
	}
	if pct >= cpuWarnPct {
		return theme.StatusWarn
	}
	return ""
}

// usagePctColor styles a "NN%" cell, returning "" to leave it unstyled.
func usagePctColor(cell, resource string) string {
	if !strings.HasSuffix(cell, "%") {
		return ""
	}
	pct, err := strconv.Atoi(strings.TrimSuffix(cell, "%"))
	if err != nil {
		return ""
	}
	return UsageStyle(pct, resource)
}

// Columns whose values read as magnitudes, so they align on the right edge.
var rightAligned = map[string]bool{"X": true, "AGE": true, "CPU%": true, "MEM%": true}

var leadingDigits = regexp.MustCompile(`^\d+`)

// alignRestarts right-aligns the numeric prefix of the RESTARTS column so the
// counts line up even when a cell carries a trailing annotation
// ("17 (3h ago)"), which plain right-alignment of the whole cell would break.
func alignRestarts(rows [][]string, col int) {
	if col < 0 {
		return
	}
	numWidth := 0
	for _, row := range rows {
		if col >= len(row) {
			continue
		}
		if match := leadingDigits.FindString(row[col]); len(match) > numWidth {
			numWidth = len(match)
		}
	}
	if numWidth == 0 {
		return
	}
	for _, row := range rows {
		if col >= len(row) {
			continue
		}
		match := leadingDigits.FindString(row[col])
		if match == "" {
			continue
		}
		row[col] = strings.Repeat(" ", numWidth-len(match)) + row[col]
	}
}

func indexOf(headers []string, name string) int {
	for i, header := range headers {
		if header == name {
			return i
		}
	}
	return -1
}

func itemLabel(count int) string {
	if count == 1 {
		return "1 item"
	}
	return strconv.Itoa(count) + " items"
}

// IndexedTable renders an indexed listing with its caption.
//
// Takes the parsed table rather than the text it would render as. The index
// service already parsed kubectl's output to number it, and re-parsing the
// padded text it produced lost anything that text cannot represent — an empty
// cell reads as column padding, so a blank the parser had recovered vanished
// again on the way here. Rows carry it intact.
func (r *Renderer) IndexedTable(table index.Table, resourceType, namespace string) {
	if !table.Indexable() {
		// Non-tabular output (JSON/YAML, or a table with no NAME column) prints
		// as-is; genuinely empty stdout (kubectl sends "No resources found" to
		// stderr) shows the zero-count caption instead of silence.
		if strings.TrimSpace(table.Raw) != "" {
			r.Raw(table.Raw)
			return
		}
		r.Caption(kinds.PluralDisplay(resourceType), namespace, itemLabel(0))
		return
	}
	if len(table.Rows) == 0 {
		r.Caption(kinds.PluralDisplay(resourceType), namespace, itemLabel(0))
		return
	}

	columns, cells := styledColumnsAndCells(table.Headers, table.Rows)

	r.Caption(kinds.PluralDisplay(resourceType), namespace, itemLabel(len(table.Rows)))
	r.Table(columns, cells)
}

// styledColumnsAndCells applies status/usage-percentage styling and
// restart-count alignment to a parsed table. Shared by IndexedTable and
// RedrawTable so a live watch table looks identical to a snapshot listing —
// a second copy of this classification would drift from it.
func styledColumnsAndCells(headers []string, rows [][]string) ([]Column, [][]Cell) {
	alignRestarts(rows, indexOf(headers, "RESTARTS"))

	statusCol := indexOf(headers, "STATUS")
	cpuCol := indexOf(headers, "CPU%")
	memCol := indexOf(headers, "MEM%")

	columns := make([]Column, len(headers))
	for i, header := range headers {
		columns[i] = Column{Header: header, Right: rightAligned[header]}
	}

	cells := make([][]Cell, 0, len(rows))
	for _, row := range rows {
		rendered := make([]Cell, len(row))
		for i, value := range row {
			switch {
			case i == statusCol:
				rendered[i] = Styled(value, statusColor(value))
			case i == cpuCol || i == memCol:
				resource := "cpu"
				if i == memCol {
					resource = "memory"
				}
				if style := usagePctColor(value, resource); style != "" {
					rendered[i] = Styled(value, style)
					continue
				}
				rendered[i] = Plain(value)
			default:
				rendered[i] = Plain(value)
			}
		}
		cells = append(cells, rendered)
	}
	return columns, cells
}

// enableNameFlex marks the NAME column to shrink and ellipsize rather than
// let a row wider than the terminal wrap onto a second physical line.
// RedrawTable's \x1b[NA cursor-up math assumes exactly one physical line
// per printed row; a wrapped row breaks that assumption, so the next
// frame's clear lands short and leaves stale fragments of the previous
// frame interleaved with the new one. No-op if there is no NAME column.
func enableNameFlex(headers []string, columns []Column) {
	if idx := indexOf(headers, "NAME"); idx >= 0 && idx < len(columns) {
		columns[idx].Flex = true
	}
}

// RedrawTable clears the previous frame (previousLines, if any) and reprints
// a caption plus a themed table built from headers/rows the same way
// IndexedTable renders a snapshot listing, returning the new line count for
// the next call. A no-op off-terminal — nothing is written and 0 is
// returned — the same way Status's spinner never runs off a terminal, so
// piped output and tests never receive cursor codes.
//
// footer, when set, is drawn under the table and redrawn with it. It is where
// the standing note about the live view goes: the redraw clears its whole
// frame each time, so anything printed under the table once would be erased on
// the next event, and anything printed above it before the loop drifts away
// from the thing it describes.
func (r *Renderer) RedrawTable(
	headers []string, rows [][]string, previousLines int, footer string, captionParts ...string,
) int {
	return r.redrawTable(headers, rows, previousLines, isTerminal(r.out), footer, captionParts...)
}

// redrawTable is RedrawTable with the terminal check injected, the same seam
// status() uses (internal/render/status.go:45) so this is testable without a
// real terminal.
func (r *Renderer) redrawTable(
	headers []string, rows [][]string, previousLines int, enabled bool,
	footer string, captionParts ...string,
) int {
	if !enabled {
		return 0
	}
	if previousLines > 0 {
		fmt.Fprintf(r.out, "\x1b[%dA\x1b[J", previousLines)
	}
	columns, cells := styledColumnsAndCells(headers, rows)
	enableNameFlex(headers, columns)
	r.Caption(captionParts...)
	r.Table(columns, cells)
	lines := 2 + len(cells) // caption line + header line + one per body row
	if footer != "" {
		r.Caption(footer)
		lines++
	}
	return lines
}

// RedrawTable is the package-level wrapper, matching every other render entry point.
func RedrawTable(
	headers []string, rows [][]string, previousLines int, footer string, captionParts ...string,
) int {
	return current.RedrawTable(headers, rows, previousLines, footer, captionParts...)
}

// Redrawing reports whether an in-place redraw actually draws, which it only
// does on a terminal.
//
// Exported for callers whose whole output is a redrawn frame: off-terminal
// RedrawTable writes nothing at all, so a note that would have sat under the
// table has to be printed on its own or the piped output explains nothing.
func Redrawing() bool { return isTerminal(current.out) }

// KeyValueTable renders a two-column listing, used for labels and annotations.
func (r *Renderer) KeyValueTable(header string, keys []string, values map[string]string) {
	if len(keys) == 0 {
		r.Caption("No " + strings.ToLower(header) + "s")
		return
	}
	columns := []Column{{Header: strings.ToUpper(header)}, {Header: "VALUE"}}
	rows := make([][]Cell, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, []Cell{Plain(key), Styled(values[key], theme.Muted)})
	}
	r.Table(columns, rows)
}

// ThemeList renders the theme registry, previewing each palette in its own
// colors rather than the active theme's, so the list shows what you'd be
// switching to.
func (r *Renderer) ThemeList(active string) {
	names := theme.Names()
	r.Caption("Themes", "", itemLabel(len(names)))

	columns := []Column{{Header: "X", Right: true}, {Header: ""}, {Header: "THEME"}, {Header: "PREVIEW"}}
	rows := make([][]Cell, 0, len(names))
	for position, name := range names {
		rowStyle := theme.Muted
		marker := ""
		if name == active {
			rowStyle = theme.Body
			marker = "→"
		}
		rows = append(rows, []Cell{
			Styled(strconv.Itoa(position+1), rowStyle),
			Styled(marker, theme.Header),
			Styled(name, rowStyle),
			Plain(r.swatch(name)),
		})
	}
	r.Table(columns, rows)
}

// EngineList renders the registered scan engines, marking the current
// default the same way ThemeList marks the active theme. There is no preview
// column — a scan engine has no visual analog to a color swatch.
func (r *Renderer) EngineList(active string) {
	names := scanner.Names()
	r.Caption("Engines", "", itemLabel(len(names)))

	columns := []Column{{Header: "X", Right: true}, {Header: ""}, {Header: "ENGINE"}}
	rows := make([][]Cell, 0, len(names))
	for position, name := range names {
		rowStyle := theme.Muted
		marker := ""
		if name == active {
			rowStyle = theme.Body
			marker = "→"
		}
		rows = append(rows, []Cell{
			Styled(strconv.Itoa(position+1), rowStyle),
			Styled(marker, theme.Header),
			Styled(name, rowStyle),
		})
	}
	r.Table(columns, rows)
}

// swatchParts are the sample words shown in a theme preview, paired with the
// style each is drawn in.
var swatchParts = []struct{ Sample, Style string }{
	{"✓ ok", theme.StatusOK},
	{"! warn", theme.StatusWarn},
	{"✗ error", theme.StatusBad},
	{"header", theme.Header},
	{"body", theme.Body},
	{"muted", theme.Muted},
}

// swatch renders a preview in the named theme's own styles.
//
// The styles are built on the active renderer's lipgloss renderer, so a preview
// degrades with everything else: piping `kx theme` must not emit color just
// because each row builds its own palette.
func (r *Renderer) swatch(name string) string {
	specs, err := theme.Styles(name)
	if err != nil {
		return ""
	}
	styles := buildStyles(r.lip, specs)
	parts := make([]string, 0, len(swatchParts))
	for _, part := range swatchParts {
		parts = append(parts, styles[part.Style].Render(part.Sample))
	}
	return strings.Join(parts, "  ")
}
