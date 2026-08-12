package render

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// An empty stack is reachable without anything being wrong: `kx ns` writes a
// slot rather than a history entry, so a fresh install can list namespaces,
// switch, and still have nothing in history. A bare header row reads as a bug,
// so the caption says what would fill it — the shape allNamespacesNote uses.
func TestStateHistoryWithNoEntriesSaysHowToFillIt(t *testing.T) {
	out := capture(func(r *Renderer) { r.StateHistory(state.History{}) })

	if !strings.Contains(out, "No history yet") {
		t.Errorf("output = %q, want it to report an empty history", out)
	}
	if !strings.Contains(out, "kx get") {
		t.Errorf("output = %q, want it to name what fills the history", out)
	}
	if strings.Contains(out, "KIND") || strings.Contains(out, "NAMESPACE") {
		t.Errorf("output = %q, want no table headers for an empty history", out)
	}
}

// The populated case keeps its count caption and its table.
func TestStateHistoryWithEntriesRendersTheTable(t *testing.T) {
	history := state.History{
		States: []state.State{{
			Resources: state.NewResources([]string{"nginx"}, kinds.Pod),
			Namespace: "prod",
		}},
		Cursor: 0,
	}
	out := capture(func(r *Renderer) { r.StateHistory(history) })

	for _, want := range []string{"1 entry", "KIND", "NAMESPACE", "Pods", "prod"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want it to contain %q", out, want)
		}
	}
}

// The context an entry was listed in is what decides whether its indexes still
// mean anything, so `kx state --all` has to show it. One shared context is a
// property of the whole listing, so it captions the table rather than repeating
// itself down a column.
func TestStateHistoryCaptionsASharedContext(t *testing.T) {
	history := state.History{
		States: []state.State{
			{Resources: state.NewResources([]string{"nginx"}, kinds.Pod),
				Namespace: "prod", Context: "docker-desktop"},
			{Resources: state.NewResources([]string{"redis"}, kinds.Pod),
				Namespace: "prod", Context: "docker-desktop"},
		},
		Cursor: 1,
	}
	out := capture(func(r *Renderer) { r.StateHistory(history) })

	if !strings.Contains(out, "docker-desktop") {
		t.Errorf("output = %q, want it to name the shared context", out)
	}
	if strings.Contains(out, "CONTEXT") {
		t.Errorf("output = %q, want no CONTEXT column when every entry shares one", out)
	}
}

// Entries from different clusters are the case the column exists for: which
// entry belongs to which context is per row, not a property of the listing.
func TestStateHistoryAddsAContextColumnWhenEntriesDiffer(t *testing.T) {
	history := state.History{
		States: []state.State{
			{Resources: state.NewResources([]string{"nginx"}, kinds.Pod),
				Namespace: "prod", Context: "staging"},
			{Resources: state.NewResources([]string{"redis"}, kinds.Pod),
				Namespace: "prod", Context: "production"},
		},
		Cursor: 1,
	}
	out := capture(func(r *Renderer) { r.StateHistory(history) })

	for _, want := range []string{"CONTEXT", "staging", "production"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want it to contain %q", out, want)
		}
	}
}

// A kubeconfig with no current context stamps nothing, and an empty column of
// empty values is worse than no column.
func TestStateHistoryOmitsAnUnknownContext(t *testing.T) {
	history := state.History{
		States: []state.State{{
			Resources: state.NewResources([]string{"nginx"}, kinds.Pod),
			Namespace: "prod",
		}},
		Cursor: 0,
	}
	out := capture(func(r *Renderer) { r.StateHistory(history) })

	if strings.Contains(out, "CONTEXT") {
		t.Errorf("output = %q, want no CONTEXT column when no entry records one", out)
	}
}

// `kx state` shows one entry, so its context goes in the caption beside the
// namespace — the same place the entry's other scope already lives.
func TestStateNamesTheEntryContext(t *testing.T) {
	out := capture(func(r *Renderer) {
		r.State(state.State{
			Resources: state.NewResources([]string{"nginx"}, kinds.Pod),
			Namespace: "prod",
			Context:   "docker-desktop",
		})
	})

	if !strings.Contains(out, "docker-desktop") {
		t.Errorf("output = %q, want the caption to name the entry's context", out)
	}
}

func slotHistory() state.History {
	return state.History{
		States: []state.State{{
			Resources: state.NewResources([]string{"nginx"}, kinds.Pod),
			Namespace: "kube-system",
		}},
		Cursor: 0,
		Named: map[kinds.Kind]state.State{
			kinds.Namespace: {
				Resources: state.NewResources(
					[]string{"default", "diagnostics", "istio-system"}, kinds.Namespace),
				Namespace: "istio-system",
			},
			kinds.Context: {
				Resources: state.NewResources([]string{"docker-desktop"}, kinds.Context),
				Namespace: "docker-desktop",
			},
		},
	}
}

// The slots are what `kx ns <n>` resolves against, so `kx state --all` has to
// show they exist — otherwise half the state kx keeps is invisible.
func TestStateHistoryListsSwitchTargets(t *testing.T) {
	out := capture(func(r *Renderer) { r.StateHistory(slotHistory()) })

	if !strings.Contains(out, "Switch targets") {
		t.Errorf("output = %q, want a switch-targets block", out)
	}
	for _, want := range []string{"Namespaces", "Contexts"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want it to contain %q", out, want)
		}
	}
	// Which kind and how many. The names in a slot and the scope it was listed
	// in both belong to the expanded view; here they would need a column that is
	// true of a namespace slot and a context slot at once.
	for _, unwanted := range []string{"diagnostics", "istio-system", "docker-desktop"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("output = %q, want %q left to --targets", out, unwanted)
		}
	}
}

// Nothing to show means no empty table: the block appears when there is one.
func TestStateHistoryOmitsSwitchTargetsWhenThereAreNone(t *testing.T) {
	history := state.History{
		States: []state.State{{
			Resources: state.NewResources([]string{"nginx"}, kinds.Pod),
			Namespace: "prod",
		}},
		Cursor: 0,
	}
	out := capture(func(r *Renderer) { r.StateHistory(history) })

	if strings.Contains(out, "Switch targets") {
		t.Errorf("output = %q, want no switch-targets block when there are no slots", out)
	}
}

// The expanded view is the one you read an index off before switching, so it
// has to carry the numbers.
func TestSwitchTargetsRendersIndexedListings(t *testing.T) {
	out := capture(func(r *Renderer) { r.SwitchTargets(slotHistory()) })

	for _, want := range []string{
		"Namespaces", "istio-system", "diagnostics", "Contexts", "docker-desktop",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want it to contain %q", out, want)
		}
	}
	if !strings.Contains(out, "NAME") {
		t.Errorf("output = %q, want an indexed listing with a NAME column", out)
	}
	// The history stack is a different view; --targets is focused.
	if strings.Contains(out, "History") {
		t.Errorf("output = %q, want no history block", out)
	}
}

func TestSwitchTargetsWithNoSlotsSaysHowToFillThem(t *testing.T) {
	out := capture(func(r *Renderer) { r.SwitchTargets(state.History{}) })

	if !strings.Contains(out, "No switch targets yet") {
		t.Errorf("output = %q, want it to report empty slots", out)
	}
	if !strings.Contains(out, "kx ns") {
		t.Errorf("output = %q, want it to name what fills them", out)
	}
}

// Slots but no stack is the fresh-install shape: `kx ns` fills a slot without
// pushing an entry. That is the moment the summary is most worth showing, so
// the empty-history note must not swallow it.
func TestStateHistoryShowsSwitchTargetsWithNoEntries(t *testing.T) {
	history := state.History{
		Named: map[kinds.Kind]state.State{
			kinds.Namespace: {
				Resources: state.NewResources([]string{"default", "prod"}, kinds.Namespace),
				Namespace: "prod",
			},
		},
	}
	out := capture(func(r *Renderer) { r.StateHistory(history) })

	if !strings.Contains(out, "No history yet") {
		t.Errorf("output = %q, want the empty-history note", out)
	}
	if !strings.Contains(out, "Switch targets") {
		t.Errorf("output = %q, want the switch-targets block alongside it", out)
	}
}
