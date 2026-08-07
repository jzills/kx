package cli

import "github.com/jzills/kx/internal/index"

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
