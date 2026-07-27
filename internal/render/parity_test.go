package render

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jzills/kx/internal/events"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// Byte-for-byte layout parity with the Python renderer.
//
// testdata/python_golden.json is captured from Rich itself (see
// scripts/gen_render_golden.py) in the no-color, wide-width mode kx uses for
// non-terminal output. Column widths, padding, alignment and caption wording
// are all pinned here, because these are exactly the details that drift when a
// table is reimplemented and that unit tests written against the new code
// would happily agree with.
//
// Delete this test, its fixtures and the generator at cutover.
type renderGolden struct {
	Indexed   string `json:"indexed"`
	Resource  string `json:"resource"`
	Namespace string `json:"namespace"`
	Output    string `json:"output"`
}

func loadRenderGolden(t *testing.T) map[string]renderGolden {
	t.Helper()
	data, err := os.ReadFile("testdata/python_golden.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var cases map[string]renderGolden
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("golden file is empty")
	}
	return cases
}

// capture renders into a buffer. A buffer is not a terminal, so styling is off
// and the comparison is pure layout.
func capture(render func(*Renderer)) string {
	var buf bytes.Buffer
	renderer := New(&buf, &buf, "github-dark", false)
	render(renderer)
	return buf.String()
}

func TestIndexedTableParity(t *testing.T) {
	cases := loadRenderGolden(t)
	for _, name := range []string{"pods", "wide_names", "no_restarts", "empty", "single_item"} {
		want, ok := cases[name]
		if !ok {
			t.Fatalf("golden is missing the %q case", name)
		}
		t.Run(name, func(t *testing.T) {
			got := capture(func(r *Renderer) {
				r.IndexedTable(want.Indexed, want.Resource, want.Namespace, "")
			})
			if got != want.Output {
				t.Errorf("layout mismatch\n go: %q\n py: %q", got, want.Output)
			}
		})
	}
}

func TestStateHistoryParity(t *testing.T) {
	want := loadRenderGolden(t)["state_history"]
	history := state.History{
		States: []state.State{
			{Resources: state.NewResources([]string{"web-1", "web-2"}, kinds.Pod), Namespace: "prod"},
			{Resources: state.NewResources([]string{"api"}, kinds.Deployment), Namespace: "staging"},
			{Resources: mixed(), Namespace: "default"},
		},
		Cursor: 1,
	}
	got := capture(func(r *Renderer) { r.StateHistory(history) })
	if got != want.Output {
		t.Errorf("layout mismatch\n go: %q\n py: %q", got, want.Output)
	}
}

// mixed builds an entry spanning two kinds, which must render as "Mixed".
func mixed() state.Resources {
	var resources state.Resources
	if err := json.Unmarshal([]byte(`{"a":"Pod","b":"Service"}`), &resources); err != nil {
		panic(err)
	}
	return resources
}

func TestKeyValueTableParity(t *testing.T) {
	cases := loadRenderGolden(t)

	got := capture(func(r *Renderer) {
		r.KeyValueTable("Label", []string{"app", "tier"}, map[string]string{
			"app": "web", "tier": "frontend",
		})
	})
	if want := cases["key_values"].Output; got != want {
		t.Errorf("layout mismatch\n go: %q\n py: %q", got, want)
	}

	got = capture(func(r *Renderer) { r.KeyValueTable("Label", nil, nil) })
	if want := cases["key_values_empty"].Output; got != want {
		t.Errorf("empty mismatch\n go: %q\n py: %q", got, want)
	}
}

// The theme list is the widest table kx renders and the only one whose cells
// arrive pre-styled, which makes it the sharpest layout check of the set.
func TestThemeListParity(t *testing.T) {
	want := loadRenderGolden(t)["theme_list"]
	got := capture(func(r *Renderer) { r.ThemeList("dracula") })
	if got != want.Output {
		t.Errorf("layout mismatch\n go: %q\n py: %q", got, want.Output)
	}
}

// Tree guides must match Rich's exactly: the structure below exercises a middle
// child, a last child, and a nested last child whose parent still continues,
// which is where a hand-rolled renderer gets the continuation bars wrong.
func TestTreeParity(t *testing.T) {
	root := &Node{Label: "Deployment/web", Style: "header"}
	rs := root.Add("rs/web-abc", "accent")
	pod1 := rs.Add("pod/web-abc-1", "body")
	pod1.Add("container: app", "muted")
	pod1.Add("container: sidecar", "muted")
	pod2 := rs.Add("pod/web-abc-2", "body")
	pod2.Add("container: app", "muted")
	root.Add("rs/web-old", "accent")

	got := capture(func(r *Renderer) { r.Tree(root) })
	if want := loadRenderGolden(t)["tree"].Output; got != want {
		t.Errorf("tree mismatch\n go: %q\n py: %q", got, want)
	}
}

func TestIndexedTreeParity(t *testing.T) {
	root := &Node{Label: "Deployment/web", Style: "header", Index: 1}
	rs := root.AddIndexed("rs/web-abc", "accent", 2)
	pod := rs.AddIndexed("pod/web-abc-1", "body", 3)
	pod.Add("container: app", "muted")

	got := capture(func(r *Renderer) { r.Tree(root) })
	if want := loadRenderGolden(t)["tree_indexed"].Output; got != want {
		t.Errorf("indexed tree mismatch\n go: %q\n py: %q", got, want)
	}
}

func TestEventsTableParity(t *testing.T) {
	now := time.Now()
	rows := []events.Row{
		{Type: "Warning", Reason: "BackOff", Kind: "Pod",
			Message: "Back-off restarting failed container", Timestamp: now.Add(-3 * time.Minute)},
		{Type: "Normal", Reason: "Pulled", Kind: "Pod",
			Message: "Container image already present", Timestamp: now.Add(-2 * time.Hour)},
		{Type: "Warning", Reason: "FailedScheduling", Kind: "Pod",
			Message: "0/1 nodes are available", Timestamp: now.Add(-24 * time.Hour)},
	}
	got := capture(func(r *Renderer) { r.EventsTable(rows) })
	if want := loadRenderGolden(t)["events"].Output; got != want {
		t.Errorf("events mismatch\n go: %q\n py: %q", got, want)
	}
}

func TestEmptyEventsParity(t *testing.T) {
	got := capture(func(r *Renderer) { r.EventsTable(nil) })
	if want := loadRenderGolden(t)["events_empty"].Output; got != want {
		t.Errorf("empty events mismatch\n go: %q\n py: %q", got, want)
	}
}
