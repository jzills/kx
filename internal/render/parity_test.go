package render

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

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
