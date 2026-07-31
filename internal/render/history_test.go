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
