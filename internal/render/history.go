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

// StateHistory renders the history stack, marking the entry the cursor is on.
func (r *Renderer) StateHistory(history state.History) {
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
