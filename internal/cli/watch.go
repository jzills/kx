package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
)

// watchRows tracks the current state of a live watch listing, keyed by
// rowKey (NAMESPACE/NAME when a NAMESPACE column is present — an -A watch —
// or NAME alone otherwise). ADDED/MODIFIED upsert a row, DELETED removes it.
// Order is insertion order: a MODIFIED row keeps its position; a key that
// reappears after DELETED is appended at the end, same as a fresh ADDED.
//
// A --match term narrows it as it narrows any listing: a row whose name the
// term does not contain is never kept, whatever its event. Dropped, kx get
// pods -w -m web watched every pod in the namespace.
//
// Each key is also numbered the first time it appears, and keeps that index
// for as long as the watch runs: a row that changes, or moves under --sort,
// is still the 3 it was, and a deleted row's index is never handed to
// another resource. A key that reappears — a StatefulSet's web-0, recreated
// — gets its old index back, since an index names a resource by name.
type watchRows struct {
	order   []string
	rows    map[string][]string
	matches func(name string) bool
	// numbers is each key's index; numbered is every key ever numbered, in
	// index order, deleted ones included, so the saved listing's nth entry
	// is the row the screen numbered n.
	numbers  map[string]int
	numbered []index.Entry
}

// newWatchRows is an empty row set narrowed by term; "" keeps every row.
func newWatchRows(term string) *watchRows {
	return &watchRows{
		rows: make(map[string][]string), matches: index.NameMatcher(term),
		numbers: make(map[string]int),
	}
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
// It reports whether the line numbered a row no index named before, which is
// when the saved listing has something new to record.
func (w *watchRows) Apply(shape index.TableShape, row []string) (numbered bool) {
	if shape.EventIdx < 0 || shape.EventIdx >= len(row) || shape.NameIdx >= len(row) {
		return false
	}
	name := row[shape.NameIdx]
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	if name == "" || !w.matches(name) {
		return false
	}
	event := row[shape.EventIdx]
	key := rowKey(shape, row)
	stored := dropIndex(row, shape.EventIdx)

	if event == "DELETED" {
		if _, ok := w.rows[key]; ok {
			delete(w.rows, key)
			w.order = removeString(w.order, key)
		}
		return false
	}

	if _, ok := w.rows[key]; !ok {
		w.order = append(w.order, key)
	}
	w.rows[key] = stored
	if _, ok := w.numbers[key]; ok {
		return false
	}
	entry := index.Entry{Name: row[shape.NameIdx]}
	if shape.NamespaceIdx >= 0 && shape.NamespaceIdx < len(row) {
		entry.Namespace = row[shape.NamespaceIdx]
	}
	w.numbered = append(w.numbered, entry)
	w.numbers[key] = len(w.numbered)
	return true
}

// NumberedSnapshot is Snapshot with each row's index appended as one more
// cell, after the last column, where a sort over the table's own columns
// never reads it (numberFirst moves it to the front once the rows are in
// order).
func (w *watchRows) NumberedSnapshot() [][]string {
	rows := make([][]string, 0, len(w.order))
	for _, key := range w.order {
		row := append([]string(nil), w.rows[key]...)
		rows = append(rows, append(row, strconv.Itoa(w.numbers[key])))
	}
	return rows
}

// Entries is every row ever numbered, in index order.
func (w *watchRows) Entries() []index.Entry { return w.numbered }

// numberFirst moves each row's trailing index cell (NumberedSnapshot) to the
// front, where kx draws its X column.
func numberFirst(rows [][]string) [][]string {
	for i, row := range rows {
		last := len(row) - 1
		rows[i] = append([]string{row[last]}, row[:last]...)
	}
	return rows
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
// context's namespace otherwise — unless a cluster-selecting flag points the
// watch elsewhere, where the current namespace is this cluster's and says
// nothing about the rows, so none is named (as GetCommand.Execute does).
func watchNamespace(extra []string, kube kubectl.Service) string {
	if allNamespaces(extra) {
		return render.AllNamespaces
	}
	if namespace := extractNamespace(extra); namespace != "" || clusterFlagIn(extra) != "" {
		return namespace
	}
	return kube.CurrentNamespace()
}

// The standing notes under a live table, drawn for as long as the watch
// runs. watchNote is the one off a terminal, where nothing is drawn or
// numbered; numberedWatchNote while the rows' indexes are the ones kx
// resolves; supersededWatchNote once another listing has replaced them.
const (
	watchNote           = "watches can't be indexed — showing a live view; press Ctrl-C to stop"
	numberedWatchNote   = "each row keeps its index while the watch runs — press Ctrl-C to stop"
	supersededWatchNote = "a newer listing replaced this watch's indexes — still watching; press Ctrl-C to stop"
)

// crossClusterWatchNote is the note under a watch of another cluster, which
// kx draws but never numbers, as it never numbers that cluster's listing.
func crossClusterWatchNote(flag string) string {
	return "watches with '" + flag + "' can't be indexed — showing a live view; press Ctrl-C to stop"
}

// watchListing saves a live watch's numbered rows as the listing kx get
// would save, each time a new row is numbered. The first save is an ordinary
// one, pushing beside the listing before it or replacing one of the same
// query. Every later save replaces that entry only while it is still the
// current one (state.Service.ReplaceCurrent): once another listing has taken
// its place, the watch's numbers are no longer what an index resolves to,
// and it stops saving for good rather than push its rows back over them.
type watchListing struct {
	state *state.Service
	entry func(entries []index.Entry) state.State
	saved *state.State
	// lost is set once another listing has replaced the watch's.
	lost bool
}

// newWatchListing is the saver for a watch of resource with the given
// pass-through args. The saved query is the plain listing — the args less
// the watch flags — so the refresh a vanished index triggers runs a listing
// that completes, and a `kx get pods` in another terminal is the same view,
// replacing this one. The context is read once, now: kubectl keeps watching
// the cluster it started on even if the kubeconfig switches under it.
func newWatchListing(services Services, resource, match, sortSpec string, extra []string) *watchListing {
	_, args := extractBool(extra, "--watch", "-w", "--watch-only", "--output-watch-events")
	namespace := ""
	if !allNamespaces(extra) && !clusterScoped(string(listingKind(resource))) {
		if namespace = extractNamespace(extra); namespace == "" {
			namespace = services.Kubectl.CurrentNamespace()
		}
	}
	context := services.Kubectl.CurrentContext()
	return &watchListing{
		state: services.State,
		entry: func(entries []index.Entry) state.State {
			listed := getListing(resource, match, args, namespace, index.Table{Entries: entries})
			listed.Query.Sort = sortSpec
			listed.Context = context
			return listed
		},
	}
}

// save records entries, the rows numbered so far, unless the watch has
// already lost its listing.
func (w *watchListing) save(headers []string, entries []index.Entry) error {
	if w.lost {
		return nil
	}
	next := w.entry(entries)
	next.Columns = listingColumns(index.Table{Headers: headers})
	if w.saved == nil {
		if err := w.state.Save(next); err != nil {
			return err
		}
		w.saved = &next
		return nil
	}
	replaced, err := w.state.ReplaceCurrent(*w.saved, next)
	if err != nil {
		return err
	}
	if !replaced {
		w.lost = true
		return nil
	}
	w.saved = &next
	return nil
}

// runWatch streams a live-redrawing table for `kx get <resource> --watch`.
// Only reached for the default/wide table shape; runGet routes non-tabular
// -o formats to the raw RunInteractive passthrough instead (see
// wantsLiveTable). -A watches are included — watchRows keys rows by
// NAMESPACE/NAME in that case, so same-named pods in different namespaces
// don't collide.
//
// A --sort orders the live table, sorted afresh from the rows as they stand
// on every redraw, so a row that changes moves to where its new cells sort.
//
// On a terminal the table is numbered and saved as it grows (watchListing),
// so `kx describe 3` in another terminal reaches the row drawn as 3. Off a
// terminal nothing is drawn, so nothing is numbered either: an index nobody
// saw would replace the listing someone did. Another cluster's watch is
// drawn unnumbered, as its listing is printed unnumbered.
func runWatch(services Services, resource string, extra []string, match, sortSpec string) error {
	keys, err := sortKeysOf(sortSpec)
	if err != nil {
		return err
	}
	namespace := watchNamespace(extra, services.Kubectl)

	args := append([]string{"get", resource}, extra...)
	if present, _ := extractBool(extra, "--output-watch-events"); !present {
		args = append(args, "--output-watch-events")
	}

	// Standing notes sit under the block they describe — the way --html's
	// "serving at ..." sits under the listing it served — so this is drawn as
	// the redraw's footer rather than printed ahead of the table. Above, it
	// scrolled away from the thing it was explaining; the refresh lead in
	// handleStale is the deliberate other case, where the text introduces a
	// listing that is not there yet.
	//
	// Off-terminal there is no redraw at all, so the note is the only output
	// there would be, and it is printed on its own instead.
	if !render.Redrawing() {
		render.Caption(watchNote)
	}
	var listing *watchListing
	footer := watchNote
	switch flag := clusterFlagIn(extra); {
	case flag != "":
		footer = crossClusterWatchNote(flag)
	case render.Redrawing():
		listing = newWatchListing(services, resource, match, sortSpec, extra)
		footer = numberedWatchNote
	}

	var shape index.TableShape
	var displayHeaders []string
	rows := newWatchRows(match)
	lines := 0

	// Redraws on every event rather than throttling: Watch's callback is
	// synchronous and there is no other trigger to catch up later, so a
	// throttle that skips a redraw can leave the screen stuck on a stale
	// frame indefinitely once the cluster goes quiet — exactly what "the
	// initial ADDED burst only shows one row" turned out to be.
	redraw := func() error {
		if listing == nil || listing.lost {
			if listing != nil {
				footer = supersededWatchNote
			}
			snapshot, err := index.SortRows(displayHeaders, rows.Snapshot(), keys)
			if err != nil {
				return sortError(err)
			}
			lines = render.RedrawTable(displayHeaders, snapshot, lines,
				footer, kinds.PluralDisplay(resource), namespace, "watching")
			return nil
		}
		// The index rides after the last column while the rows are sorted,
		// so a sort orders them by their own cells and each keeps its number.
		snapshot, err := index.SortRows(displayHeaders, rows.NumberedSnapshot(), keys)
		if err != nil {
			return sortError(err)
		}
		lines = render.RedrawTable(append([]string{"X"}, displayHeaders...), numberFirst(snapshot), lines,
			footer, kinds.PluralDisplay(resource), namespace, "watching")
		return nil
	}

	return services.Kubectl.Watch(args, func(line string) error {
		if displayHeaders == nil {
			parsed, ok := index.ParseHeader(line)
			if !ok || parsed.EventIdx < 0 {
				return fmt.Errorf("kubectl's watch output has no EVENT column, so kx cannot draw it.")
			}
			shape = parsed
			displayHeaders = dropIndex(shape.Headers, shape.EventIdx)
			// Refused on the header, before a row is drawn, when a key names
			// a column the watch has none of.
			if _, err := index.SortRows(displayHeaders, nil, keys); err != nil {
				return sortError(err)
			}
			return nil
		}
		if strings.TrimSpace(line) == "" {
			return nil
		}
		if rows.Apply(shape, shape.Row(line)) && listing != nil {
			if err := listing.save(append([]string{"X"}, displayHeaders...), rows.Entries()); err != nil {
				return err
			}
		}
		return redraw()
	})
}
