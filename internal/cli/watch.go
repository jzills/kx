package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
)

// watchRows tracks the current state of a live watch listing, keyed by NAME
// (single-namespace only — a namespace-wide watch, -A, keeps the raw
// streaming passthrough instead, since name alone isn't a safe row key
// across namespaces). ADDED/MODIFIED upsert a row, DELETED removes it.
// Order is insertion order: a MODIFIED row keeps its position; a name that
// reappears after DELETED is appended at the end, same as a fresh ADDED.
type watchRows struct {
	order []string
	rows  map[string][]string
}

func newWatchRows() *watchRows {
	return &watchRows{rows: make(map[string][]string)}
}

// Apply updates the row set from one parsed line. row is the full slice
// shape.Row returned, including the EVENT and NAME cells at shape's indexes.
func (w *watchRows) Apply(shape index.TableShape, row []string) {
	if shape.EventIdx < 0 || shape.EventIdx >= len(row) || shape.NameIdx >= len(row) {
		return
	}
	event := row[shape.EventIdx]
	name := row[shape.NameIdx]
	if name == "" {
		return
	}
	stored := dropIndex(row, shape.EventIdx)

	if event == "DELETED" {
		if _, ok := w.rows[name]; ok {
			delete(w.rows, name)
			w.order = removeString(w.order, name)
		}
		return
	}

	if _, ok := w.rows[name]; !ok {
		w.order = append(w.order, name)
	}
	w.rows[name] = stored
}

// Snapshot returns the current rows in display order, EVENT column already
// stripped, as fresh copies — callers (render.RedrawTable's styling) mutate
// rows in place, and that must never reach watchRows' stored state.
func (w *watchRows) Snapshot() [][]string {
	rows := make([][]string, 0, len(w.order))
	for _, name := range w.order {
		rows = append(rows, append([]string(nil), w.rows[name]...))
	}
	return rows
}

// dropIndex returns row with the element at idx removed, leaving row itself
// untouched.
func dropIndex(row []string, idx int) []string {
	out := make([]string, 0, len(row)-1)
	for i, v := range row {
		if i != idx {
			out = append(out, v)
		}
	}
	return out
}

// removeString returns list with every occurrence of s removed, leaving list
// itself untouched.
func removeString(list []string, s string) []string {
	out := list[:0:0]
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

// watchThrottle caps how often a burst of watch events triggers a redraw —
// well under human perception (~10/sec), so an initial ADDED burst for an
// existing namespace doesn't flicker while updates still feel live.
const watchThrottle = 100 * time.Millisecond

// runWatch streams a live-redrawing table for `kx get <resource> --watch`.
// Only reached for the default/wide, single-namespace table shape; runGet
// routes non-tabular -o formats and -A to the raw RunInteractive passthrough
// instead (see wantsLiveTable).
func runWatch(services Services, resource string, extra []string) error {
	namespace := extractNamespace(extra)
	if namespace == "" {
		namespace = services.Kubectl.CurrentNamespace()
	}

	args := append([]string{"get", resource}, extra...)
	if present, _ := extractBool(extra, "--output-watch-events"); !present {
		args = append(args, "--output-watch-events")
	}

	render.Caption("watches can't be indexed — showing a live view; press Ctrl-C to stop")

	var shape index.TableShape
	var displayHeaders []string
	rows := newWatchRows()
	lines := 0
	lastDraw := time.Time{}

	redraw := func(force bool) {
		if !force && time.Since(lastDraw) < watchThrottle {
			return
		}
		lastDraw = time.Now()
		lines = render.RedrawTable(displayHeaders, rows.Snapshot(), lines,
			kinds.PluralDisplay(resource), namespace, "watching")
	}

	err := services.Kubectl.Watch(args, func(line string) error {
		if displayHeaders == nil {
			parsed, ok := index.ParseHeader(line)
			if !ok || parsed.EventIdx < 0 {
				return fmt.Errorf("kx get --watch: unexpected kubectl output (no EVENT column)")
			}
			shape = parsed
			displayHeaders = dropIndex(shape.Headers, shape.EventIdx)
			return nil
		}
		if strings.TrimSpace(line) == "" {
			return nil
		}
		rows.Apply(shape, shape.Row(line))
		redraw(false)
		return nil
	})
	if displayHeaders != nil {
		redraw(true)
	}
	return err
}
