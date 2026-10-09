package cli

import (
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// stateDocument is kx state --json: the entry indexes resolve against now.
//
// Built from state.State rather than by marshalling it, so ~/.kx/state.json
// and this document can each change without breaking the other. The file is a
// compatibility surface for kx's own reads; this is the one for everyone
// else's.
//
// Context is where the caller is now, read from kubeconfig. Beside each
// entry's own context it is what tells a consumer whether that entry's indexes
// still resolve here — kx refuses an index counted in another cluster.
type stateDocument struct {
	SchemaVersion int        `json:"schemaVersion"`
	Context       string     `json:"context,omitempty"`
	Entry         *stateJSON `json:"entry"`
}

// stateHistoryDocument is kx state --all --json: the whole stack, in the order
// kx state --all lists it.
type stateHistoryDocument struct {
	SchemaVersion int         `json:"schemaVersion"`
	Context       string      `json:"context,omitempty"`
	Entries       []stateJSON `json:"entries"`
}

// stateJSON is one history entry.
//
// Position is 1-based, the number kx state N takes. Namespace is absent for a
// cluster-scoped listing and for one taken with -A, which says so with
// AllNamespaces instead. ByAgent is list_marks's name for the same tag: the
// listing was saved by kx mcp on an agent's behalf.
type stateJSON struct {
	Position      int             `json:"position"`
	Current       bool            `json:"current"`
	Context       string          `json:"context,omitempty"`
	Namespace     string          `json:"namespace,omitempty"`
	AllNamespaces bool            `json:"allNamespaces,omitempty"`
	Query         *stateQueryJSON `json:"query"`
	ByAgent       bool            `json:"byAgent,omitempty"`
	Resources     []stateRowJSON  `json:"resources"`
}

// stateQueryJSON is the listing that produced an entry. Command is always
// spelled out: the file leaves it empty for kx get, which is a detail of how
// old files read, not something a consumer should have to know.
type stateQueryJSON struct {
	Command  string   `json:"command"`
	Resource string   `json:"resource"`
	Args     []string `json:"args"`
	Match    *string  `json:"match,omitempty"`
}

// stateRowJSON is one row: what kx ref would print for its index, as fields.
type stateRowJSON struct {
	Index     int        `json:"index"`
	Kind      kinds.Kind `json:"kind"`
	Name      string     `json:"name"`
	Namespace string     `json:"namespace,omitempty"`
}

// stateJSONOf converts one entry at its 0-based place in the stack.
func stateJSONOf(entry state.State, place, cursor int) stateJSON {
	rows := make([]stateRowJSON, 0, entry.Resources.Len())
	for i, resource := range entry.Resources.Entries() {
		rows = append(rows, stateRowJSON{
			Index: i + 1, Kind: resource.Kind, Name: resource.Name,
			// The same lookup an index resolves through, so this cannot
			// disagree with kx ref about where a row lives.
			Namespace: entry.NamespaceAt(i + 1),
		})
	}
	document := stateJSON{
		Position:      place + 1,
		Current:       place == cursor,
		Context:       entry.Context,
		Namespace:     entry.Namespace,
		AllNamespaces: entry.AllNamespaces,
		ByAgent:       entry.Source == state.SourceMCP,
		Resources:     rows,
	}
	if query := entry.Query; query != nil {
		command := query.Command
		if command == "" {
			command = "get"
		}
		args := query.Args
		if args == nil {
			args = []string{}
		}
		document.Query = &stateQueryJSON{
			Command: command, Resource: query.Resource, Args: args, Match: query.Match,
		}
	}
	return document
}

// stateEntryJSON serialises the entry at the cursor, or a null entry for an
// empty stack.
func stateEntryJSON(history state.History, context string) (string, error) {
	document := stateDocument{SchemaVersion: reportSchemaVersion, Context: context}
	if len(history.States) > 0 {
		entry := stateJSONOf(history.States[history.Cursor], history.Cursor, history.Cursor)
		document.Entry = &entry
	}
	return encode(document)
}

// stateHistoryJSON serialises the whole stack.
func stateHistoryJSON(history state.History, context string) (string, error) {
	entries := make([]stateJSON, 0, len(history.States))
	for place, entry := range history.States {
		entries = append(entries, stateJSONOf(entry, place, history.Cursor))
	}
	return encode(stateHistoryDocument{
		SchemaVersion: reportSchemaVersion, Context: context, Entries: entries,
	})
}
