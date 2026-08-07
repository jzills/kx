package render

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/theme"
)

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
// Takes the text produced by the index service and re-parses it rather than
// taking structured rows, so the caller stays a thin pass-through of whatever
// columns kubectl chose to emit.
func (r *Renderer) IndexedTable(text, resourceType, namespace, note string) {
	headers, rows, _ := index.ParseTable(text)
	if headers == nil {
		// Non-tabular output (JSON/YAML, or a table with no NAME column) prints
		// as-is; genuinely empty stdout (kubectl sends "No resources found" to
		// stderr) shows the zero-count caption instead of silence.
		if strings.TrimSpace(text) != "" {
			r.Raw(text)
			return
		}
		r.Caption(kinds.PluralDisplay(resourceType), namespace, itemLabel(0))
		return
	}
	if len(rows) == 0 {
		r.Caption(kinds.PluralDisplay(resourceType), namespace, itemLabel(0))
		return
	}

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

	r.Caption(kinds.PluralDisplay(resourceType), namespace, itemLabel(len(rows)))
	r.Table(columns, cells)
	if note != "" {
		r.Caption(note)
	}
}

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
//
// Every cell renders pre-styled via Plain rather than Styled: a Styled cell's
// semantic name resolves against the *active* renderer's style map
// (Table -> r.style), which would color every row in the active theme's
// colors regardless of which palette that row is meant to preview — exactly
// wrong for a listing whose entire point is showing other palettes. A theme
// calibrated for a light terminal background (e.g. "light") rendered as the
// active theme then paints every row's text in near-black, unreadable on the
// typical dark terminal background this process can't detect or control.
func (r *Renderer) ThemeList(active string) {
	names := theme.Names()
	r.Caption("Themes", "", itemLabel(len(names)))

	columns := []Column{{Header: "X", Right: true}, {Header: ""}, {Header: "THEME"}, {Header: "PREVIEW"}}
	rows := make([][]Cell, 0, len(names))
	for position, name := range names {
		styles := r.paletteStyles(name)
		styleName := theme.Muted
		marker := ""
		if name == active {
			styleName = theme.Body
			marker = "→"
		}
		rows = append(rows, []Cell{
			Plain(styles[styleName].Render(strconv.Itoa(position + 1))),
			Plain(styles[theme.Header].Render(marker)),
			Plain(styles[styleName].Render(name)),
			Plain(r.swatch(styles)),
		})
	}
	r.Table(columns, rows)
}

// paletteStyles builds the named theme's styles on the active renderer's
// lipgloss renderer, so a row degrades with everything else: piping `kx
// theme` must not emit color just because each row previews its own palette.
// Falls back to the active renderer's own styles if name is somehow
// unregistered (Names() is theme's own registry, so this should not happen),
// rather than leaving the row fully unstyled.
func (r *Renderer) paletteStyles(name string) map[string]lipgloss.Style {
	specs, err := theme.Styles(name)
	if err != nil {
		return r.styles
	}
	return buildStyles(r.lip, specs)
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

// swatch renders a preview in the given theme's own styles (see
// paletteStyles).
func (r *Renderer) swatch(styles map[string]lipgloss.Style) string {
	parts := make([]string, 0, len(swatchParts))
	for _, part := range swatchParts {
		parts = append(parts, styles[part.Style].Render(part.Sample))
	}
	return strings.Join(parts, "  ")
}
