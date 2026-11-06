package cli

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

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
	// full is set once a row went unnumbered because maxWatchIndexes rows
	// already had indexes.
	full bool
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
	if len(w.numbered) >= maxWatchIndexes {
		w.full = true
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
// order). A row past maxWatchIndexes has an empty one.
func (w *watchRows) NumberedSnapshot() [][]string {
	rows := make([][]string, 0, len(w.order))
	for _, key := range w.order {
		row := append([]string(nil), w.rows[key]...)
		number := ""
		if n, ok := w.numbers[key]; ok {
			number = strconv.Itoa(n)
		}
		rows = append(rows, append(row, number))
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
// one), and otherwise the namespace its listing records (listingNamespace) —
// unless a cluster-selecting flag points the watch elsewhere, where the
// current namespace is this cluster's and says nothing about the rows, so
// only a typed one is named (as GetCommand.Execute does).
func watchNamespace(resource string, extra []string, scope string, kube kubectl.Service) string {
	if allNamespaces(extra) {
		return render.AllNamespaces
	}
	if clusterFlagIn(extra) != "" {
		return extractNamespace(extra)
	}
	return listingNamespace(kube, resource, extra, scope)
}

// The standing notes under a live table, drawn for as long as the watch
// runs, each a fact and then what the screen is doing, short enough to fit
// an 80-column terminal on one line. watchNote is the one off a terminal,
// where nothing is drawn or numbered, and numberedWatchNote the one while
// the rows' indexes are the ones kx resolves. The rest say why the numbers
// went: another listing became current; the same listing ran again — a
// refresh after a deleted row's index was used, or a kx get in another
// terminal; the saved listings were cleared, by kx state drop --all or a
// schema reset; or the state file could not be written or read.
const (
	watchNote             = "watches can't be indexed — showing a live view; press Ctrl-C to stop"
	numberedWatchNote     = "rows keep their indexes — showing a live view; press Ctrl-C to stop"
	supersededWatchNote   = "another listing is now current — showing a live view; press Ctrl-C to stop"
	relistedWatchNote     = "a rerun renumbered these rows — showing a live view; press Ctrl-C to stop"
	clearedWatchNote      = "the saved listings were cleared — showing a live view; press Ctrl-C to stop"
	unkeptWatchNote       = "these indexes couldn't be kept — showing a live view; press Ctrl-C to stop"
	crossClusterWatchNote = "another cluster isn't indexed — showing a live view; press Ctrl-C to stop"
)

// fullWatchNote is the note once a watch has numbered n rows, the most it
// numbers (maxWatchIndexes).
func fullWatchNote(n int) string {
	return fmt.Sprintf("rows past %d aren't indexed — showing a live view; press Ctrl-C to stop", n)
}

// maxWatchIndexes is the most rows a watch numbers. A deleted row keeps its
// index, so the saved listing only grows, and every save rewrites all of it:
// a day's watch over CronJob Pods would otherwise write a file of thousands
// of dead names each second. Past it, a new row is drawn without a number
// rather than any number changing meaning. A variable so a test can reach it
// without a thousand rows.
var maxWatchIndexes = 1000

// watchTicker calls tick about once a second until stop is called. It is
// how a watch saves a burst of new rows in one write, and how a quiet one
// notices that another listing has replaced it — a watch whose rows only
// change state otherwise has no event to notice it on. A variable so a test
// can tick the watch itself, between two of its events.
//
// stop returns only once no tick is running or will run, so nothing is drawn
// or saved after the watch has returned.
var watchTicker = func(tick func()) (stop func()) {
	ticker := time.NewTicker(time.Second)
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				select {
				case <-done:
					return
				default:
					tick()
				}
			}
		}
	}()
	return func() {
		ticker.Stop()
		close(done)
		<-exited
	}
}

// watchListing saves a live watch's numbered rows as the listing kx get
// would save. The first save (start) is an ordinary one, made as the watch
// begins and before it has a row, pushing beside the listing before it or
// replacing one of the same query. Every later save replaces
// that entry only while it is still the current one
// (state.Service.ReplaceCurrent), and between saves each tick checks that it
// still is. Once another listing has taken its place the watch's numbers are
// no longer what an index resolves to, so it stops for good, ended naming
// why, rather than push its rows back over that listing.
type watchListing struct {
	state *state.Service
	entry func(entries []index.Entry) state.State
	saved *state.State
	// dirty is set when a row was numbered since the last save.
	dirty bool
	// ended is the note saying why the watch stopped numbering, "" while it
	// still does.
	ended string
}

// newWatchListing is the saver for a watch of resource with the given
// pass-through args, scope being the namespace of the listing an index came
// from, as GetCommand.Scope is. The saved query is the plain listing — the
// args less the watch flags, and without scope, which the listing records as
// its namespace as kx get's does — so the refresh a deleted row's index
// triggers runs a listing that completes, and a kx get pods in another
// terminal is the same view. The context is read once, now: kubectl keeps
// watching the cluster it started on even if the kubeconfig switches.
func newWatchListing(services Services, resource, match, sortSpec, scope string, extra []string) *watchListing {
	_, args := extractBool(extra, "--watch", "-w", "--watch-only", "--output-watch-events")
	namespace := listingNamespace(services.Kubectl, resource, extra, scope)
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

// start claims the current listing as the watch begins, before kubectl has
// sent anything. `kx get` saves even when it found nothing, for the reason
// GetCommand.Execute gives: an empty listing that saved no entry left the
// *previous* listing resolving indexes. A watch that deferred its first save
// until a row was numbered had the same hole, and held it open for as long as
// the namespace stayed quiet — `kx get jobs -w` on an idle namespace, a `kx
// get svc` in another terminal, and the Job that turned up minutes later
// pushed its row over that Service listing without a word, so `kx describe 1`
// there reached the Job.
//
// Saving here closes it: the watch is the current listing from the start, so
// every later write is a ReplaceCurrent and a listing that lands meanwhile is
// noticed on the next tick. It is also what makes the first frame's note
// true — a state file kx cannot write says so before the screen draws
// numbers nothing resolves.
func (w *watchListing) start() { w.save(nil, nil) }

// tick saves the rows numbered so far if any is new, and otherwise checks
// the watch's entry is still the current one. It reports whether the watch
// stopped numbering, which the screen has to show.
func (w *watchListing) tick(headers []string, entries []index.Entry) (ended bool) {
	if w.ended != "" {
		return false
	}
	if w.dirty {
		w.save(headers, entries)
	} else if w.saved != nil {
		current, err := w.state.IsCurrent(*w.saved)
		switch {
		case err != nil:
			w.fail(err)
		case !current:
			w.end()
		}
	}
	return w.ended != ""
}

// save writes entries. A write that fails ends the numbering, not the watch:
// before watches were numbered one never touched the state file, and a home
// directory kx cannot write to is no reason to lose the live view.
func (w *watchListing) save(headers []string, entries []index.Entry) {
	next := w.entry(entries)
	next.Columns = listingColumns(index.Table{Headers: headers})
	w.dirty = false
	if w.saved == nil {
		if err := w.state.Save(next); err != nil {
			w.fail(err)
			return
		}
		w.saved = &next
		return
	}
	replaced, err := w.state.ReplaceCurrent(*w.saved, next)
	switch {
	case err != nil:
		w.fail(err)
	case !replaced:
		w.end()
	default:
		w.saved = &next
	}
}

// fail stops the numbering because the state file could not be used: gone
// or reset, it says the listings were cleared, which is not another listing
// taking over; anything else, that the indexes could not be kept.
func (w *watchListing) fail(err error) {
	w.ended = unkeptWatchNote
	if errors.Is(err, state.ErrNoState) || errors.Is(err, state.ErrSchemaChanged) {
		w.ended = clearedWatchNote
	}
}

// end stops the numbering because another entry replaced the watch's,
// naming a re-run of the watch's own query apart from another listing.
func (w *watchListing) end() {
	w.ended = supersededWatchNote
	current, err := w.state.Load()
	if err != nil {
		w.fail(err)
		return
	}
	if current.Query != nil && reflect.DeepEqual(*current.Query, *w.saved.Query) {
		w.ended = relistedWatchNote
	}
}

// watchSession is one live watch: the rows, the listing it saves, and the
// frame it last drew. The watch's events and its ticks arrive on different
// goroutines, so each takes mu for everything it touches.
type watchSession struct {
	mu      sync.Mutex
	rows    *watchRows
	listing *watchListing
	keys    []index.SortKey
	shape   index.TableShape
	// headers are kubectl's less EVENT; numbered are headers behind X.
	headers  []string
	numbered []string
	caption  []string
	// footer is the note when nothing is numbered.
	footer string
	lines  int
}

// numbering reports whether the screen shows indexes kx resolves.
func (s *watchSession) numbering() bool {
	return s.listing != nil && s.listing.ended == ""
}

// note is the footer for the frame about to be drawn.
func (s *watchSession) note() string {
	switch {
	case s.listing == nil:
		return s.footer
	case s.listing.ended != "":
		return s.listing.ended
	case s.rows.full:
		return fullWatchNote(maxWatchIndexes)
	}
	return numberedWatchNote
}

// Redraws on every event rather than throttling: Watch's callback is
// synchronous and there is no other trigger to catch up later, so a
// throttle that skips a redraw can leave the screen stuck on a stale frame
// indefinitely once the cluster goes quiet — exactly what "the initial
// ADDED burst only shows one row" turned out to be.
func (s *watchSession) redraw() error {
	// One snapshot, the frame's own: both copy every row, and a redraw runs
	// on every event, so taking the plain one first and discarding it under
	// numbering — the ordinary terminal case — copied a table of up to
	// maxWatchIndexes rows twice per event to draw it once.
	var headers []string
	var rows [][]string
	if s.numbering() {
		// The index rides after the last column while the rows are sorted,
		// so a sort orders them by their own cells and each keeps its number.
		headers, rows = s.numbered, s.rows.NumberedSnapshot()
	} else {
		headers, rows = s.headers, s.rows.Snapshot()
	}
	sorted, err := index.SortRows(s.headers, rows, s.keys)
	if err != nil {
		return sortError(err)
	}
	if s.numbering() {
		sorted = numberFirst(sorted)
	}
	s.lines = render.RedrawTable(headers, sorted, s.lines, s.note(), s.caption...)
	return nil
}

// line takes one line of kubectl's watch output.
func (s *watchSession) line(line string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.headers == nil {
		parsed, ok := index.ParseHeader(line)
		if !ok || parsed.EventIdx < 0 {
			return fmt.Errorf("kubectl's watch output has no EVENT column, so kx cannot draw it.")
		}
		s.shape = parsed
		s.headers = dropIndex(s.shape.Headers, s.shape.EventIdx)
		s.numbered = append([]string{"X"}, s.headers...)
		// Refused on the header, before a row is drawn, when a key names
		// a column the watch has none of.
		if _, err := index.SortRows(s.headers, nil, s.keys); err != nil {
			return sortError(err)
		}
		return nil
	}
	if strings.TrimSpace(line) == "" {
		return nil
	}
	if s.rows.Apply(s.shape, s.shape.Row(line)) && s.listing != nil {
		s.listing.dirty = true
	}
	return s.redraw()
}

// tick is the listing's tick, redrawn when it stopped the numbering.
func (s *watchSession) tick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listing == nil || s.headers == nil {
		return
	}
	if s.listing.tick(s.numbered, s.rows.Entries()) {
		_ = s.redraw()
	}
}

// runWatch streams a live-redrawing table for `kx get <resource> --watch`.
// Only reached for the default/wide table shape; runGet routes non-tabular
// -o formats to the raw RunInteractive passthrough instead (see
// wantsLiveTable). -A watches are included — watchRows keys rows by
// NAMESPACE/NAME in that case, so same-named pods in different namespaces
// don't collide. scope is the namespace of the listing an index came from,
// as GetCommand.Scope is, and kubectl is asked under it.
//
// A --sort orders the live table, sorted afresh from the rows as they stand
// on every redraw, so a row that changes moves to where its new cells sort.
//
// On a terminal the table is numbered: the listing is saved as the watch
// starts, and again within about a second of a row being numbered
// (watchListing), so `kx describe 3` in another terminal reaches the row
// drawn as 3. Off a terminal nothing is drawn, so nothing is
// numbered either: an index nobody saw would replace the listing someone
// did. Another cluster's watch is drawn unnumbered, as its listing is
// printed unnumbered.
func runWatch(services Services, resource string, extra []string, scope, match, sortSpec string) error {
	keys, err := sortKeysOf(sortSpec)
	if err != nil {
		return err
	}
	session := &watchSession{
		rows: newWatchRows(match), keys: keys, footer: watchNote,
		caption: []string{kinds.PluralDisplay(resource), watchNamespace(resource, extra, scope, services.Kubectl), "watching"},
	}

	scoped := append(append([]string(nil), extra...), replayScope(scope, extra)...)
	args := append([]string{"get", resource}, scoped...)
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
	switch {
	case clusterFlagIn(extra) != "":
		session.footer = crossClusterWatchNote
	case render.Redrawing():
		session.listing = newWatchListing(services, resource, match, sortSpec, scope, extra)
		session.listing.start()
		stop := watchTicker(session.tick)
		// A watch kubectl ends saves what it numbered since the last tick.
		// Ctrl-C ends kx with it, so at most a second's new rows go unsaved.
		defer session.tick()
		defer stop()
	}
	return services.Kubectl.Watch(args, session.line)
}
