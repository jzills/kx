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
	r.Caption("History", "", entryLabel(len(history.States)))

	columns := []Column{
		{Header: "X", Right: true},
		{Header: ""},
		{Header: "KIND"},
		{Header: "NAMESPACE"},
		{Header: "ITEMS", Right: true},
	}
	rows := make([][]Cell, 0, len(history.States))
	for position, entry := range history.States {
		rowStyle := theme.Muted
		marker := ""
		if position == history.Cursor {
			rowStyle = theme.Body
			marker = "→"
		}
		rows = append(rows, []Cell{
			Styled(strconv.Itoa(position+1), rowStyle),
			Styled(marker, theme.Header),
			Styled(kindLabel(entry.Resources), rowStyle),
			Styled(entry.Namespace, rowStyle),
			Styled(strconv.Itoa(entry.Resources.Len()), rowStyle),
		})
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
			Styled(entry.Namespace, theme.Muted),
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
		{Header: "NAMESPACE"},
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
	r.Caption(kindLabel(entry.Resources), entry.Namespace, itemLabel(count))

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
