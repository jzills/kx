package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jzills/kx/internal/kinds"
)

// Upgrade compatibility: a ~/.kx/state.json written by the Python
// implementation must keep working, with indexes resolving to the same
// resources they did before. testdata/python_state.json was produced by the
// real kx.state.StateService — note "web-1", "web-2", "api-3" are deliberately
// not in sorted order, so a map-backed port resolves index 1 to the wrong pod.
//
// Delete this test along with the Python implementation at cutover.
func loadPythonState(t *testing.T) *Service {
	t.Helper()
	source, err := os.ReadFile("testdata/python_state.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, source, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	service := NewService(10)
	service.Path = path
	return service
}

func TestReadsPythonWrittenState(t *testing.T) {
	service := loadPythonState(t)

	current, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// The fixture's cursor is 1, the deployment listing.
	if names := current.Names(); len(names) != 1 || names[0] != "api" {
		t.Errorf("Names() = %v, want [api]", names)
	}
	if kind, _ := current.Resources.Kind("api"); kind != kinds.Deployment {
		t.Errorf("Kind(api) = %q, want Deployment", kind)
	}
	if current.Query == nil || current.Query.Match == nil || *current.Query.Match != "api" {
		t.Errorf("Query did not survive: %+v", current.Query)
	}
}

func TestPythonStateIndexOrderPreserved(t *testing.T) {
	service := loadPythonState(t)
	if _, err := service.Navigate(-1); err != nil {
		t.Fatalf("Navigate: %v", err)
	}

	want := []string{"web-1", "web-2", "api-3"}
	for i, wantName := range want {
		name, namespace, kind, err := service.Fields(i + 1)
		if err != nil {
			t.Fatalf("Fields(%d): %v", i+1, err)
		}
		if name != wantName {
			t.Errorf("Fields(%d) = %q, want %q — index order did not survive the read",
				i+1, name, wantName)
		}
		if namespace != "prod" || kind != kinds.Pod {
			t.Errorf("Fields(%d) = _, %q, %q; want prod, Pod", i+1, namespace, kind)
		}
	}
}

// Writing back a Python-written file must not reorder or drop anything, so the
// two implementations can be used against the same state file during the port.
func TestRewritingPythonStatePreservesOrder(t *testing.T) {
	service := loadPythonState(t)
	if _, err := service.Navigate(-1); err != nil {
		t.Fatalf("Navigate: %v", err)
	}

	data, err := os.ReadFile(service.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var raw struct {
		States []struct {
			Resources json.RawMessage `json:"resources"`
		} `json:"states"`
		Cursor int `json:"cursor"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("rewritten state is unparseable: %v", err)
	}
	if raw.Cursor != 0 {
		t.Errorf("Cursor = %d, want 0 after navigating back", raw.Cursor)
	}
	want := `{"web-1":"Pod","web-2":"Pod","api-3":"Pod"}`
	if got := string(raw.States[0].Resources); got != want {
		t.Errorf("resources rewritten as %s, want %s", got, want)
	}
}
