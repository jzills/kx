package cli

import (
	"fmt"
	"strings"

	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
)

// watchRows tracks the current state of a live watch listing, keyed by
// rowKey (NAMESPACE/NAME when a NAMESPACE column is present — an -A watch —
// or NAME alone otherwise). ADDED/MODIFIED upsert a row, DELETED removes it.
// Order is insertion order: a MODIFIED row keeps its position; a key that
// reappears after DELETED is appended at the end, same as a fresh ADDED.
type watchRows struct {
	order []string
	rows  map[string][]string
}

func newWatchRows() *watchRows {
	return &watchRows{rows: make(map[string][]string)}
}

// rowKey identifies a row uniquely. Names alone collide across namespaces —
// two different namespaces can each have a pod called "worker-0" — so an -A
// watch (NamespaceIdx present) keys on NAMESPACE/NAME instead of NAME alone.
func rowKey(shape index.TableShape, row []string) string {
	name := row[shape.NameIdx]
	if shape.NamespaceIdx >= 0 && shape.NamespaceIdx < len(row) {
		return row[shape.NamespaceIdx] + "/" + name
	}
	return name
}

// Apply updates the row set from one parsed line. row is the full slice
// shape.Row returned, including the EVENT and NAME cells at shape's indexes.
func (w *watchRows) Apply(shape index.TableShape, row []string) {
	if shape.EventIdx < 0 || shape.EventIdx >= len(row) || shape.NameIdx >= len(row) {
		return
	}
	if row[shape.NameIdx] == "" {
		return
	}
	event := row[shape.EventIdx]
	key := rowKey(shape, row)
	stored := dropIndex(row, shape.EventIdx)

	if event == "DELETED" {
		if _, ok := w.rows[key]; ok {
			delete(w.rows, key)
			w.order = removeString(w.order, key)
		}
		return
	}

	if _, ok := w.rows[key]; !ok {
		w.order = append(w.order, key)
	}
	w.rows[key] = stored
}

// Snapshot returns the current rows in display order, EVENT column already
// stripped, as fresh copies — callers (render.RedrawTable's styling) mutate
// rows in place, and that must never reach watchRows' stored state.
func (w *watchRows) Snapshot() [][]string {
	rows := make([][]string, 0, len(w.order))
	for _, key := range w.order {
		rows = append(rows, append([]string(nil), w.rows[key]...))
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

// watchNamespace resolves the caption namespace for a watch listing: "all
// namespaces" for -A (rows there are keyed by NAMESPACE/NAME, not scoped to
// one), the explicit -n/--namespace value if given, or the current
// context's namespace otherwise.
func watchNamespace(extra []string, kube kubectl.Service) string {
	if allNamespaces(extra) {
		return render.AllNamespaces
	}
	if namespace := extractNamespace(extra); namespace != "" {
		return namespace
	}
	return kube.CurrentNamespace()
}

// runWatch streams a live-redrawing table for `kx get <resource> --watch`.
// Only reached for the default/wide table shape; runGet routes non-tabular
// -o formats to the raw RunInteractive passthrough instead (see
// wantsLiveTable). -A watches are included — watchRows keys rows by
// NAMESPACE/NAME in that case, so same-named pods in different namespaces
// don't collide.
func runWatch(services Services, resource string, extra []string) error {
	namespace := watchNamespace(extra, services.Kubectl)

	args := append([]string{"get", resource}, extra...)
	if present, _ := extractBool(extra, "--output-watch-events"); !present {
		args = append(args, "--output-watch-events")
	}

	render.Caption("watches can't be indexed — showing a live view; press Ctrl-C to stop")

	var shape index.TableShape
	var displayHeaders []string
	rows := newWatchRows()
	lines := 0

	// Redraws on every event rather than throttling: Watch's callback is
	// synchronous and there is no other trigger to catch up later, so a
	// throttle that skips a redraw can leave the screen stuck on a stale
	// frame indefinitely once the cluster goes quiet — exactly what "the
	// initial ADDED burst only shows one row" turned out to be.
	redraw := func() {
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
		redraw()
		return nil
	})
	return err
}
