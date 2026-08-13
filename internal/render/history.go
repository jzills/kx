package render

import (
	"strconv"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
	"github.com/jzills/kx/internal/theme"
)

// kindLabel names a listing by the kind it holds, or "Mixed" when an entry
// spans several — a namespace-wide tree, for instance.
func kindLabel(resources state.Resources) string {
	var seen kinds.Kind
	for i, entry := range resources.Entries() {
		if i == 0 {
			seen = entry.Kind
			continue
		}
		if entry.Kind != seen {
			return "Mixed"
		}
	}
	if seen == "" {
		return "Mixed"
	}
	return kinds.PluralDisplay(string(seen))
}

func entryLabel(count int) string {
	if count == 1 {
		return "1 entry"
	}
	return strconv.Itoa(count) + " entries"
}

// emptyHistoryNote explains an empty stack, since `kx ns` writing a slot rather
// than a history entry means you can reach this without anything being wrong.
const emptyHistoryNote = "No history yet — run kx get <resource> to start one"

// AllNamespaces is how kx names a listing that spans them, wherever a single
// namespace would otherwise be shown.
const AllNamespaces = "all namespaces"

// scopeLabel names the scope an entry was listed in.
//
// A listing that spans namespaces records none on the entry, because there is
// no single one to record — each resource carries its own. Left as the empty
// string it would render as a blank column and drop out of the caption
// entirely, which reads as missing data rather than as the scope it is.
func scopeLabel(entry state.State) string {
	if entry.Resources.Spanning() {
		return AllNamespaces
	}
	return entry.Namespace
}

// spansContexts reports whether the stack holds entries from more than one
// context, which is the only shape where the context belongs in a column.
//
// Deliberately blind to whether the contexts are empty: a stack that records
// none agrees with itself, and must not be mistaken for one that disagrees.
func spansContexts(states []state.State) bool {
	for _, entry := range states {
		if entry.Context != states[0].Context {
			return true
		}
	}
	return false
}

// sharedContext returns the one context every entry was listed in, or "" when
// they disagree — that stack gets the column instead — or when none recorded
// one, which has nothing to say either way.
func sharedContext(states []state.State) string {
	if len(states) == 0 || spansContexts(states) {
		return ""
	}
	return states[0].Context
}

// StateHistory renders the history stack, marking the entry the cursor is on.
func (r *Renderer) StateHistory(history state.History) {
	if len(history.States) == 0 {
		// Caption alone, no header row, matching how an empty listing renders.
		// The slots still show: an empty stack with a filled namespace slot is
		// exactly what a fresh install has after `kx ns`, and that is the moment
		// the summary is most worth reading.
		r.Caption(emptyHistoryNote)
		r.switchTargetSummary(history)
		return
	}
	// The context decides whether an entry's indexes still mean anything, so it
	// has to be visible — but where depends on the stack. One context shared by
	// every entry is a property of the listing and captions it; entries from
	// different clusters need it per row. A stack that records none (no current
	// context in the kubeconfig) shows neither, since a column of empty values
	// is worse than no column.
	r.Caption("History", sharedContext(history.States), entryLabel(len(history.States)))

	perRow := spansContexts(history.States)
	columns := []Column{
		{Header: "X", Right: true},
		{Header: ""},
		{Header: "KIND"},
		{Header: "NAMESPACE"},
	}
	if perRow {
		columns = append(columns, Column{Header: "CONTEXT"})
	}
	columns = append(columns, Column{Header: "ITEMS", Right: true})

	rows := make([][]Cell, 0, len(history.States))
	for position, entry := range history.States {
		rowStyle := theme.Muted
		marker := ""
		if position == history.Cursor {
			rowStyle = theme.Body
			marker = "→"
		}
		row := []Cell{
			Styled(strconv.Itoa(position+1), rowStyle),
			Styled(marker, theme.Header),
			Styled(kindLabel(entry.Resources), rowStyle),
			Styled(scopeLabel(entry), rowStyle),
		}
		if perRow {
			row = append(row, Styled(entry.Context, rowStyle))
		}
		rows = append(rows, append(row, Styled(strconv.Itoa(entry.Resources.Len()), rowStyle)))
	}
	r.Table(columns, rows)
	r.switchTargetSummary(history)
}

// slotOrder fixes the order the slots render in, so repeated runs agree. Map
// iteration would shuffle them.
var slotOrder = []kinds.Kind{kinds.Namespace, kinds.Context}

// emptyTargetsNote explains empty slots the way emptyHistoryNote explains an
// empty stack.
const emptyTargetsNote = "No switch targets yet — run kx ns or kx contexts to fill them"

// switchTargetSummary lists the slots under the history table.
//
// The slots are what `kx ns <n>` resolves against, and they are not in the
// stack, so without this half the state kx keeps would be invisible. A summary
// rather than the listings themselves: it sits at the same altitude as the
// table above it, and `kx state --targets` is the expanded view.
//
// Which kind and how many, and nothing else. The scope each listing was taken
// in has no column that is true of both rows — a context slot records a context
// where a namespace slot records a namespace — and it is already carried by the
// expanded view's caption, where it is positional and claims nothing.
//
// Nothing to show means no block at all — an empty table under a full history
// reads as breakage rather than as an absence.
func (r *Renderer) switchTargetSummary(history state.History) {
	rows := make([][]Cell, 0, len(slotOrder))
	for _, kind := range slotOrder {
		entry, ok := history.Named[kind]
		if !ok {
			continue
		}
		rows = append(rows, []Cell{
			Styled(kindLabel(entry.Resources), theme.Muted),
			Styled(strconv.Itoa(entry.Resources.Len()), theme.Muted),
		})
	}
	if len(rows) == 0 {
		return
	}
	r.Blank()
	r.Caption("Switch targets")
	r.Table([]Column{
		{Header: "KIND"},
		{Header: "ITEMS", Right: true},
	}, rows)
}

// SwitchTargets renders each slot as a full indexed listing, which is what
// `kx state --targets` shows.
//
// The summary answers "is there a namespace slot, and from where"; this answers
// "what does `kx ns 2` switch to", which is the question you have just before
// switching. Reuses State so a slot listing looks like every other listing.
func (r *Renderer) SwitchTargets(history state.History) {
	rendered := 0
	for _, kind := range slotOrder {
		entry, ok := history.Named[kind]
		if !ok {
			continue
		}
		if rendered > 0 {
			r.Blank()
		}
		r.State(entry)
		rendered++
	}
	if rendered == 0 {
		r.Caption(emptyTargetsNote)
	}
}

// State renders a single history entry as an indexed name/kind listing, which
// is what `kx state` shows.
func (r *Renderer) State(entry state.State) {
	count := entry.Resources.Len()
	// The context sits beside the namespace: both say where the listing was
	// taken, and Caption drops either when it is empty.
	r.Caption(kindLabel(entry.Resources), scopeLabel(entry), entry.Context, itemLabel(count))

	columns := []Column{{Header: "X", Right: true}, {Header: "KIND"}, {Header: "NAME"}}
	rows := make([][]Cell, 0, count)
	for position, resource := range entry.Resources.Entries() {
		rows = append(rows, []Cell{
			Plain(strconv.Itoa(position + 1)),
			Plain(string(resource.Kind)),
			Plain(resource.Name),
		})
	}
	r.Table(columns, rows)
}
