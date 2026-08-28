package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
)

func newTestService(t *testing.T, maxHistory int) *Service {
	t.Helper()
	service := NewService(maxHistory)
	service.Path = filepath.Join(t.TempDir(), "state.json")
	return service
}

func pods(names ...string) Resources {
	return NewResources(names, kinds.Pod)
}

func save(t *testing.T, service *Service, state State) {
	t.Helper()
	if err := service.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("nginx", "redis"), Namespace: "prod"})

	loaded, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Namespace != "prod" {
		t.Errorf("Namespace = %q, want %q", loaded.Namespace, "prod")
	}
	names := loaded.Names()
	if len(names) != 2 || names[0] != "nginx" || names[1] != "redis" {
		t.Errorf("Names() = %v, want [nginx redis]", names)
	}
	if kind, ok := loaded.Resources.Kind("nginx"); !ok || kind != kinds.Pod {
		t.Errorf("Kind(nginx) = %q, %v; want Pod, true", kind, ok)
	}
}

// Index resolution is positional: index n is the nth resource. A Go map would
// reorder these on every load, silently pointing indexes at the wrong resource.
// The round trip must be order-stable across repeated loads, not just once.
func TestResourceOrderIsStableAcrossLoads(t *testing.T) {
	service := newTestService(t, 10)
	want := []string{"zulu", "alpha", "mike", "bravo", "yankee", "charlie", "x-ray", "delta"}
	save(t, service, State{Resources: pods(want...), Namespace: "default"})

	for attempt := 0; attempt < 50; attempt++ {
		loaded, err := service.Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		got := loaded.Names()
		if len(got) != len(want) {
			t.Fatalf("len(Names()) = %d, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("attempt %d: Names()[%d] = %q, want %q (order not preserved)",
					attempt, i, got[i], want[i])
			}
		}
	}
}

// The on-disk shape is this build's compatibility surface: a version mismatch
// resets the file (see TestVersionMismatchResetsFile) rather than trying to
// stay compatible with every historical shape, so this test pins the current
// version's shape rather than promising it never changes.
func TestSavedJSONMatchesOnDiskSchema(t *testing.T) {
	service := newTestService(t, 10)
	service.Context = func() string { return "docker-desktop" }
	match := "web"
	save(t, service, State{
		Resources: pods("nginx"),
		Namespace: "prod",
		Query:     &Query{Resource: "pods", Args: []string{"-n", "prod"}, Match: &match},
	})

	data, err := os.ReadFile(service.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var raw struct {
		Version int `json:"version"`
		States  []struct {
			Resources []struct {
				Name      string `json:"name"`
				Kind      string `json:"kind"`
				Namespace string `json:"namespace"`
			} `json:"resources"`
			Namespace string `json:"namespace"`
			Query     *struct {
				Resource string   `json:"resource"`
				Args     []string `json:"args"`
				Match    *string  `json:"match"`
			} `json:"query"`
			Context string `json:"context"`
		} `json:"states"`
		Cursor int `json:"cursor"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("saved state is not the expected schema: %v\n%s", err, data)
	}
	if raw.Version != currentSchemaVersion {
		t.Errorf("version = %d, want %d", raw.Version, currentSchemaVersion)
	}
	if len(raw.States) != 1 || len(raw.States[0].Resources) != 1 ||
		raw.States[0].Resources[0].Name != "nginx" || raw.States[0].Resources[0].Kind != "Pod" {
		t.Errorf("resources = %v, want [{nginx Pod}]", raw.States[0].Resources)
	}
	if raw.Cursor != 0 {
		t.Errorf("cursor = %d, want 0", raw.Cursor)
	}
	if raw.States[0].Query == nil || raw.States[0].Query.Resource != "pods" {
		t.Errorf("query did not round-trip: %+v", raw.States[0].Query)
	}
	if raw.States[0].Query.Match == nil || *raw.States[0].Query.Match != "web" {
		t.Errorf("query match did not round-trip")
	}
	if raw.States[0].Context != "docker-desktop" {
		t.Errorf("context = %q, want %q", raw.States[0].Context, "docker-desktop")
	}
}

func TestQueryOmittedIsNull(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("nginx"), Namespace: "default"})

	loaded, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Query != nil {
		t.Errorf("Query = %+v, want nil", loaded.Query)
	}
}

func TestLoadMissingFileReturnsErrNoState(t *testing.T) {
	service := newTestService(t, 10)
	if _, err := service.Load(); err != ErrNoState {
		t.Errorf("Load on missing file = %v, want ErrNoState", err)
	}
}

// A version mismatch — including the total absence of a version key, which is
// every file written before versioning existed — resets state.json rather
// than trying to keep reading an old or foreign shape. The next Load must see
// an ordinary empty state, not another error.
func TestVersionMismatchResetsFile(t *testing.T) {
	service := newTestService(t, 10)
	stale := `{"version":99,"states":[{"resources":[{"name":"nginx","kind":"Pod"}],"namespace":"prod"}],"cursor":0}`
	if err := os.WriteFile(service.Path, []byte(stale), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := service.Load(); err != ErrSchemaChanged {
		t.Fatalf("Load on a version-mismatched file = %v, want ErrSchemaChanged", err)
	}

	data, err := os.ReadFile(service.Path)
	if err != nil {
		t.Fatalf("ReadFile after reset: %v", err)
	}
	var raw struct {
		Version int              `json:"version"`
		States  []map[string]any `json:"states"`
		Cursor  int              `json:"cursor"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("reset file is not valid JSON: %v\n%s", err, data)
	}
	if raw.Version != currentSchemaVersion {
		t.Errorf("reset version = %d, want %d", raw.Version, currentSchemaVersion)
	}
	if len(raw.States) != 0 {
		t.Errorf("reset states = %v, want empty", raw.States)
	}

	if _, err := service.Load(); err != ErrNoState {
		t.Errorf("Load after reset = %v, want ErrNoState", err)
	}
}

// A file with no "version" key at all — the shape of every state.json written
// before this change — is exactly as stale as a wrong-version one and gets the
// same reset treatment, not a legacy read path.
func TestMissingVersionKeyResetsFile(t *testing.T) {
	service := newTestService(t, 10)
	preVersioning := `{"states":[{"resources":[{"name":"nginx","kind":"Pod"}],"namespace":"prod"}],"cursor":0}`
	if err := os.WriteFile(service.Path, []byte(preVersioning), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := service.Load(); err != ErrSchemaChanged {
		t.Fatalf("Load on a pre-versioning file = %v, want ErrSchemaChanged", err)
	}
	if _, err := service.Load(); err != ErrNoState {
		t.Errorf("Load after reset = %v, want ErrNoState", err)
	}
}

// The pre-history single-entry shape (no "states" key at all) also predates
// versioning, so it resets exactly like any other unversioned file rather
// than being transparently loaded.
func TestLegacySingleStateFormatResetsFile(t *testing.T) {
	service := newTestService(t, 10)
	legacy := `{"resources": {"nginx": "Pod"}, "namespace": "prod"}`
	if err := os.WriteFile(service.Path, []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := service.Load(); err != ErrSchemaChanged {
		t.Fatalf("Load on a legacy file = %v, want ErrSchemaChanged", err)
	}
	if _, err := service.Load(); err != ErrNoState {
		t.Errorf("Load after reset = %v, want ErrNoState", err)
	}
}

// Save self-heals a version-mismatched file exactly as it does a corrupt one:
// `kx get <resource>` rebuilds state without surfacing the reset to the user.
func TestSaveHealsVersionMismatchedFile(t *testing.T) {
	service := newTestService(t, 10)
	stale := `{"version":99,"states":[{"resources":[{"name":"nginx","kind":"Pod"}],"namespace":"prod"}],"cursor":0}`
	if err := os.WriteFile(service.Path, []byte(stale), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	save(t, service, State{Resources: pods("redis"), Namespace: "default"})

	loaded, err := service.Load()
	if err != nil {
		t.Fatalf("Load after heal: %v", err)
	}
	if len(loaded.Names()) != 1 || loaded.Names()[0] != "redis" {
		t.Errorf("healed state = %+v", loaded)
	}
}

func TestCorruptFileReturnsActionableError(t *testing.T) {
	for name, contents := range map[string]string{
		"invalid json": "{not json",
		"missing keys": `{"version": 2, "states": [{"namespace": "prod"}], "cursor": 0}`,
	} {
		t.Run(name, func(t *testing.T) {
			service := newTestService(t, 10)
			if err := os.WriteFile(service.Path, []byte(contents), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			_, err := service.Load()
			if err == nil {
				t.Fatal("Load succeeded on a corrupt file")
			}
			if !strings.Contains(err.Error(), "kx get <resource>") {
				t.Errorf("error = %q, want it to name the recovery step", err)
			}
		})
	}
}

// A corrupt file must not wedge kx: the next `kx get` overwrites it.
func TestSaveHealsCorruptFile(t *testing.T) {
	service := newTestService(t, 10)
	if err := os.WriteFile(service.Path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	save(t, service, State{Resources: pods("nginx"), Namespace: "default"})

	loaded, err := service.Load()
	if err != nil {
		t.Fatalf("Load after heal: %v", err)
	}
	if len(loaded.Names()) != 1 {
		t.Errorf("healed state = %+v", loaded)
	}
}

func TestHistoryPreservesPreviousStates(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("first"), Namespace: "default"})
	save(t, service, State{Resources: pods("second"), Namespace: "default"})

	history, err := service.LoadHistory()
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(history.States) != 2 {
		t.Fatalf("len(States) = %d, want 2", len(history.States))
	}
	if history.Cursor != 1 {
		t.Errorf("Cursor = %d, want 1", history.Cursor)
	}
}

func TestHistoryCapDropsOldest(t *testing.T) {
	service := newTestService(t, 2)
	for _, name := range []string{"one", "two", "three"} {
		save(t, service, State{Resources: pods(name), Namespace: "default"})
	}

	history, err := service.LoadHistory()
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(history.States) != 2 {
		t.Fatalf("len(States) = %d, want 2", len(history.States))
	}
	if history.States[0].Names()[0] != "two" {
		t.Errorf("oldest retained = %q, want %q", history.States[0].Names()[0], "two")
	}
}

// max_history < 1 would make the trim a no-op and grow the file without bound.
func TestZeroMaxHistoryKeepsAtLeastOne(t *testing.T) {
	service := NewService(0)
	service.Path = filepath.Join(t.TempDir(), "state.json")
	save(t, service, State{Resources: pods("one"), Namespace: "default"})
	save(t, service, State{Resources: pods("two"), Namespace: "default"})

	history, err := service.LoadHistory()
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(history.States) != 1 {
		t.Errorf("len(States) = %d, want 1", len(history.States))
	}
}

func TestNavigateBackAndForward(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("first"), Namespace: "default"})
	save(t, service, State{Resources: pods("second"), Namespace: "default"})

	back, err := service.Navigate(-1)
	if err != nil {
		t.Fatalf("Navigate(-1): %v", err)
	}
	if back.Names()[0] != "first" {
		t.Errorf("Navigate(-1) = %q, want first", back.Names()[0])
	}

	forward, err := service.Navigate(1)
	if err != nil {
		t.Fatalf("Navigate(1): %v", err)
	}
	if forward.Names()[0] != "second" {
		t.Errorf("Navigate(1) = %q, want second", forward.Names()[0])
	}
}

func TestNavigateClampsAtEnds(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("only"), Namespace: "default"})

	for _, delta := range []int{-5, 5} {
		state, err := service.Navigate(delta)
		if err != nil {
			t.Fatalf("Navigate(%d): %v", delta, err)
		}
		if state.Names()[0] != "only" {
			t.Errorf("Navigate(%d) = %q, want only", delta, state.Names()[0])
		}
	}
}

func TestNavigatePersistsCursor(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("first"), Namespace: "default"})
	save(t, service, State{Resources: pods("second"), Namespace: "default"})
	if _, err := service.Navigate(-1); err != nil {
		t.Fatalf("Navigate: %v", err)
	}

	loaded, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Names()[0] != "first" {
		t.Errorf("cursor did not persist: Load() = %q", loaded.Names()[0])
	}
}

// Saving after navigating back discards the forward entries, so history stays a
// single line rather than branching.
func TestSaveAfterBackTruncatesForwardHistory(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("first"), Namespace: "default"})
	save(t, service, State{Resources: pods("second"), Namespace: "default"})
	if _, err := service.Navigate(-1); err != nil {
		t.Fatalf("Navigate: %v", err)
	}
	save(t, service, State{Resources: pods("third"), Namespace: "default"})

	history, err := service.LoadHistory()
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(history.States) != 2 {
		t.Fatalf("len(States) = %d, want 2", len(history.States))
	}
	if history.States[1].Names()[0] != "third" {
		t.Errorf("States[1] = %q, want third", history.States[1].Names()[0])
	}
}

func TestNavigateTo(t *testing.T) {
	service := newTestService(t, 10)
	for _, name := range []string{"one", "two", "three"} {
		save(t, service, State{Resources: pods(name), Namespace: "default"})
	}

	cases := map[int]string{1: "one", 2: "two", 0: "one", 99: "three"}
	for position, want := range cases {
		state, err := service.NavigateTo(position)
		if err != nil {
			t.Fatalf("NavigateTo(%d): %v", position, err)
		}
		if state.Names()[0] != want {
			t.Errorf("NavigateTo(%d) = %q, want %q", position, state.Names()[0], want)
		}
	}
}

func TestDropRemovesEntry(t *testing.T) {
	service := newTestService(t, 10)
	for _, name := range []string{"one", "two", "three"} {
		save(t, service, State{Resources: pods(name), Namespace: "default"})
	}

	history, err := service.Drop(2)
	if err != nil {
		t.Fatalf("Drop: %v", err)
	}
	if len(history.States) != 2 {
		t.Fatalf("len(States) = %d, want 2", len(history.States))
	}
	for _, state := range history.States {
		if state.Names()[0] == "two" {
			t.Errorf("dropped entry is still present")
		}
	}
}

// Dropping an entry before the cursor shifts it, so the cursor keeps pointing
// at the same listing.
func TestDropBeforeCursorDecrementsCursor(t *testing.T) {
	service := newTestService(t, 10)
	for _, name := range []string{"one", "two", "three"} {
		save(t, service, State{Resources: pods(name), Namespace: "default"})
	}

	history, err := service.Drop(1)
	if err != nil {
		t.Fatalf("Drop: %v", err)
	}
	if history.Cursor != 1 {
		t.Errorf("Cursor = %d, want 1", history.Cursor)
	}
	if history.States[history.Cursor].Names()[0] != "three" {
		t.Errorf("cursor moved off the current listing")
	}
}

func TestDropOnlyEntryFails(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("only"), Namespace: "default"})

	if _, err := service.Drop(1); err == nil {
		t.Error("Drop succeeded on the only entry, want an error")
	}
}

// DropAll clears the whole file — the navigation stack and the
// namespace/context slots together — since they live in one History struct
// and "clear everything" is read literally.
func TestDropAllClearsHistoryAndSlots(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("one"), Namespace: "default"})
	save(t, service, State{Resources: pods("two"), Namespace: "default"})
	if err := service.SaveNamed(State{Resources: namespaces("default", "prod"), Namespace: "default"}); err != nil {
		t.Fatalf("SaveNamed: %v", err)
	}

	if err := service.DropAll(); err != nil {
		t.Fatalf("DropAll: %v", err)
	}

	history, err := service.LoadHistory()
	if err != nil {
		t.Fatalf("LoadHistory after DropAll: %v", err)
	}
	if len(history.States) != 0 {
		t.Errorf("len(States) = %d, want 0", len(history.States))
	}
	if len(history.Named) != 0 {
		t.Errorf("len(Named) = %d, want 0", len(history.Named))
	}
}

// DropAll on a state file that has never been written must not error — a
// fresh install has nothing to clear, and that's not a failure.
func TestDropAllOnAbsentStateFileSucceeds(t *testing.T) {
	service := newTestService(t, 10)
	if err := service.DropAll(); err != nil {
		t.Fatalf("DropAll on an absent state file: %v", err)
	}
	if _, err := service.LoadHistory(); err != nil {
		t.Fatalf("LoadHistory after DropAll: %v", err)
	}
}

func TestFieldsResolvesIndex(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("nginx", "redis"), Namespace: "prod"})

	name, namespace, kind, err := service.Fields(2)
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	if name != "redis" || namespace != "prod" || kind != kinds.Pod {
		t.Errorf("Fields(2) = %q, %q, %q; want redis, prod, Pod", name, namespace, kind)
	}
}

func TestFieldsOutOfRange(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("nginx"), Namespace: "prod"})

	if _, _, _, err := service.Fields(9); err == nil {
		t.Error("Fields(9) succeeded, want an out-of-range error")
	}
}

func TestCountReturnsTheCurrentListingSize(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("nginx", "redis", "web"), Namespace: "prod"})

	count, err := service.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 3 {
		t.Errorf("Count() = %d, want 3", count)
	}
}

func TestCountWithNoStateReturnsErrNoState(t *testing.T) {
	service := newTestService(t, 10)

	if _, err := service.Count(); err != ErrNoState {
		t.Errorf("Count() with no state = %v, want ErrNoState", err)
	}
}

// The regression this whole change exists for: two resources of different
// kinds sharing a name must both be indexed and resolve to their own correct
// kind, surviving a real on-disk round trip — not just the in-memory struct.
func TestSameNameDifferentKindBothSurviveSaveAndLoad(t *testing.T) {
	service := newTestService(t, 10)
	entries := []Resource{
		{Name: "waypoint", Kind: kinds.Deployment},
		{Name: "waypoint", Kind: kinds.Service},
	}
	save(t, service, State{Resources: NewOrderedResources(entries), Namespace: "prod"})

	// Load through a fresh Service pointed at the same path, so this exercises
	// the wire format and not just the struct Save was handed.
	reloaded := &Service{MaxHistory: 10, Path: service.Path}

	name1, _, kind1, err := reloaded.Fields(1)
	if err != nil {
		t.Fatalf("Fields(1): %v", err)
	}
	if name1 != "waypoint" || kind1 != kinds.Deployment {
		t.Errorf("Fields(1) = %q/%q, want waypoint/Deployment", name1, kind1)
	}

	name2, _, kind2, err := reloaded.Fields(2)
	if err != nil {
		t.Fatalf("Fields(2): %v", err)
	}
	if name2 != "waypoint" || kind2 != kinds.Service {
		t.Errorf("Fields(2) = %q/%q, want waypoint/Service", name2, kind2)
	}
}

func TestResourcesAtIsOneBasedAndBoundsChecked(t *testing.T) {
	r := NewOrderedResources([]Resource{
		{Name: "first", Kind: kinds.Pod},
		{Name: "last", Kind: kinds.Service},
	})

	if _, ok := r.At(0); ok {
		t.Error("At(0) = ok, want false — At is 1-based")
	}
	if _, ok := r.At(3); ok {
		t.Error("At(3) = ok, want false — past the end")
	}
	if entry, ok := r.At(1); !ok || entry.Name != "first" {
		t.Errorf("At(1) = %+v, %v; want first, true", entry, ok)
	}
	if entry, ok := r.At(2); !ok || entry.Name != "last" {
		t.Errorf("At(2) = %+v, %v; want last, true", entry, ok)
	}
}

func TestPreviousLists(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: NewResources([]string{"web"}, kinds.Deployment), Namespace: "default"})
	save(t, service, State{Resources: pods("web-abc"), Namespace: "default"})

	if !service.PreviousLists(kinds.Deployment) {
		t.Error("PreviousLists(Deployment) = false, want true")
	}
	if service.PreviousLists(kinds.Service) {
		t.Error("PreviousLists(Service) = true, want false")
	}
}

// Best-effort: PreviousLists only decorates an error message, so an unreadable
// history must yield no hint rather than displacing the real error.
func TestPreviousListsOnMissingStateIsFalse(t *testing.T) {
	service := newTestService(t, 10)
	if service.PreviousLists(kinds.Pod) {
		t.Error("PreviousLists on missing state = true, want false")
	}
}

// The messages an index command gets when it has named its kind.
//
// Fields resolves the index before the kind is checked, so these two shapes
// used to be reported without reference to the kind asked for: an out-of-range
// index described whatever listing was current, and an empty history said to
// run `kx get <resource>` for a command that knew the resource.
func TestFieldsExpectingNamesTheKindOnEveryFailure(t *testing.T) {
	t.Run("out of range names the current listing and the relist", func(t *testing.T) {
		service := newTestService(t, 10)
		save(t, service, State{
			Resources: NewResources([]string{"api"}, kinds.Service),
			Namespace: "prod",
		})

		_, _, err := service.FieldsExpecting(2, kinds.Namespace)
		if err == nil {
			t.Fatal("index 2 of a 1-item listing resolved")
		}
		for _, want := range []string{
			"Index 2 is out of range",
			"the current listing has 1 Service",
			"kx get namespace",
			"Namespaces",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("err = %q\n  missing %q", err, want)
			}
		}
	})

	t.Run("no state names the kind rather than <resource>", func(t *testing.T) {
		service := newTestService(t, 10)

		_, _, err := service.FieldsExpecting(1, kinds.Deployment)
		if err == nil {
			t.Fatal("resolved against no state")
		}
		if !strings.Contains(err.Error(), "kx get deployment") {
			t.Errorf("err = %q, want it to name the deployment relist", err)
		}
		if strings.Contains(err.Error(), "<resource>") {
			t.Errorf("err = %q, still the generic message", err)
		}
	})

	t.Run("wrong kind keeps the existing message", func(t *testing.T) {
		service := newTestService(t, 10)
		save(t, service, State{Resources: pods("nginx"), Namespace: "prod"})

		_, _, err := service.FieldsExpecting(1, kinds.Namespace)
		if err == nil {
			t.Fatal("a Pod resolved as a Namespace")
		}
		if !strings.Contains(err.Error(), "Index 1 is Pod/nginx, not Namespace") {
			t.Errorf("err = %q, want the existing mismatch wording", err)
		}
	})

	t.Run("a match resolves", func(t *testing.T) {
		service := newTestService(t, 10)
		save(t, service, State{
			Resources: NewResources([]string{"a", "b"}, kinds.Namespace),
			Namespace: "prod",
		})

		name, namespace, err := service.FieldsExpecting(2, kinds.Namespace)
		if err != nil {
			t.Fatalf("FieldsExpecting: %v", err)
		}
		if name != "b" || namespace != "prod" {
			t.Errorf("got %q in %q, want b in prod", name, namespace)
		}
	})
}

// A mixed entry has no single kind to name, so the count is described in
// neutral terms rather than mislabelled as one of them.
func TestDescribeCurrentOnAMixedListing(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{
		Resources: NewOrderedResources([]Resource{
			{Name: "web", Kind: kinds.Deployment},
			{Name: "web-abc", Kind: kinds.Pod},
		}),
		Namespace: "prod",
	})

	_, _, err := service.FieldsExpecting(9, kinds.Namespace)
	if err == nil {
		t.Fatal("index 9 resolved")
	}
	if !strings.Contains(err.Error(), "2 items") {
		t.Errorf("err = %q, want a neutral count for a mixed listing", err)
	}
}

func namespaces(names ...string) Resources {
	return NewResources(names, kinds.Namespace)
}

// The reported sequence from #156: a namespace listing, an unrelated listing on
// top of it, then a switch. The switch resolves against the namespace slot, so
// the intervening pods never enter into it.
func TestNamedSlotSurvivesAnInterveningListing(t *testing.T) {
	service := newTestService(t, 10)
	if err := service.SaveNamed(State{Resources: namespaces("default", "prod"), Namespace: "default"}); err != nil {
		t.Fatalf("SaveNamed: %v", err)
	}
	save(t, service, State{Resources: pods("nginx", "redis"), Namespace: "default"})

	name, _, err := service.FieldsNamed(2, kinds.Namespace)
	if err != nil {
		t.Fatalf("FieldsNamed: %v", err)
	}
	if name != "prod" {
		t.Errorf("FieldsNamed(2) = %q, want %q", name, "prod")
	}
}

// SaveNamed is the `kx ns` path: it must not push onto the history stack, which
// is the whole point of the slot.
func TestSaveNamedDoesNotTouchHistory(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("nginx"), Namespace: "default"})
	if err := service.SaveNamed(State{Resources: namespaces("default", "prod"), Namespace: "default"}); err != nil {
		t.Fatalf("SaveNamed: %v", err)
	}

	history, err := service.LoadHistory()
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(history.States) != 1 {
		t.Fatalf("len(States) = %d, want 1 — SaveNamed pushed onto history", len(history.States))
	}
	current, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := current.Names(); len(got) != 1 || got[0] != "nginx" {
		t.Errorf("current entry = %v, want [nginx]", got)
	}
}

// `kx get ns` is the escape hatch for describing or labelling a namespace, so it
// writes both: history (for `kx describe <n>`) and the slot (for `kx ns <n>`).
func TestSaveOfANamespaceListingPopulatesBoth(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: namespaces("default", "prod"), Namespace: "default"})

	name, _, kind, err := service.Fields(2)
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	if name != "prod" || kind != kinds.Namespace {
		t.Errorf("Fields(2) = %q/%q, want prod/Namespace", kind, name)
	}
	named, _, err := service.FieldsNamed(2, kinds.Namespace)
	if err != nil {
		t.Fatalf("FieldsNamed: %v", err)
	}
	if named != "prod" {
		t.Errorf("FieldsNamed(2) = %q, want %q", named, "prod")
	}
}

// A listing of anything else leaves the namespace slot alone.
func TestSaveOfAnotherKindDoesNotPopulateTheSlot(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("nginx"), Namespace: "default"})

	if _, _, err := service.FieldsNamed(1, kinds.Namespace); err == nil {
		t.Fatal("FieldsNamed on an unpopulated slot succeeded, want an error")
	}
}

// A file holding slots and no stack is a shape that did not exist before slots:
// `kx ns` on a fresh install writes one, ahead of any `kx get`. It is
// well-formed, so every stack reader has to report an empty stack rather than
// the corruption an entry-less "states" used to mean.
func TestSlotsOnlyFileReadsAsAnEmptyStack(t *testing.T) {
	service := newTestService(t, 10)
	if err := service.SaveNamed(State{
		Resources: namespaces("default", "prod"), Namespace: "default",
	}); err != nil {
		t.Fatalf("SaveNamed: %v", err)
	}

	for name, read := range map[string]func() error{
		"Load":       func() error { _, err := service.Load(); return err },
		"Navigate":   func() error { _, err := service.Navigate(-1); return err },
		"NavigateTo": func() error { _, err := service.NavigateTo(1); return err },
		"Drop":       func() error { _, err := service.Drop(1); return err },
		"Fields":     func() error { _, _, _, err := service.Fields(1); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := read(); err != ErrNoState {
				t.Errorf("%s on a slots-only file = %v, want ErrNoState", name, err)
			}
		})
	}

	// Empty stack, not an empty file: what `kx ns 2` reads is still there.
	if name, _, err := service.FieldsNamed(2, kinds.Namespace); err != nil || name != "prod" {
		t.Errorf("FieldsNamed(2) = %q, %v; want prod, nil", name, err)
	}
	// And nothing above wrote the cursor into a stack that has no entry to
	// point at — Navigate and Drop both save when they succeed.
	if _, err := service.LoadHistory(); err != nil {
		t.Errorf("LoadHistory after the stack readers ran: %v", err)
	}
}

// The shape resolves itself: the first `kx get` after a fresh-install `kx ns`
// builds a stack on top of the slot rather than starting the file over.
func TestSaveOntoASlotsOnlyFileBuildsTheStack(t *testing.T) {
	service := newTestService(t, 10)
	if err := service.SaveNamed(State{
		Resources: namespaces("default", "prod"), Namespace: "default",
	}); err != nil {
		t.Fatalf("SaveNamed: %v", err)
	}
	save(t, service, State{Resources: pods("nginx"), Namespace: "prod"})

	current, err := service.Load()
	if err != nil {
		t.Fatalf("Load after the first get: %v", err)
	}
	if got := current.Names(); len(got) != 1 || got[0] != "nginx" {
		t.Errorf("current entry = %v, want [nginx]", got)
	}
	if name, _, err := service.FieldsNamed(2, kinds.Namespace); err != nil || name != "prod" {
		t.Errorf("FieldsNamed(2) = %q, %v; want prod, nil — the slot was dropped", name, err)
	}
}

// The eviction defect that sank the per-kind history search: the slot is not in
// the stack, so a full stack cannot displace it.
func TestNamedSlotSurvivesHistoryEviction(t *testing.T) {
	service := newTestService(t, 2)
	if err := service.SaveNamed(State{Resources: namespaces("default", "prod"), Namespace: "default"}); err != nil {
		t.Fatalf("SaveNamed: %v", err)
	}
	for i := 0; i < 5; i++ {
		save(t, service, State{Resources: pods("nginx"), Namespace: "default"})
	}

	name, _, err := service.FieldsNamed(2, kinds.Namespace)
	if err != nil {
		t.Fatalf("FieldsNamed after eviction: %v", err)
	}
	if name != "prod" {
		t.Errorf("FieldsNamed(2) = %q, want %q", name, "prod")
	}
}

// An empty slot must say how to fill it rather than falling back to the current
// listing, which is the bug this whole change removes.
func TestFieldsNamedOnEmptySlotNamesTheRelist(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("nginx"), Namespace: "default"})

	_, _, err := service.FieldsNamed(1, kinds.Namespace)
	if err == nil {
		t.Fatal("FieldsNamed on an empty slot succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "kx ns") {
		t.Errorf("error = %q, want it to name 'kx ns'", err.Error())
	}
}

// Out of range against the slot reports the slot's own size, not the current
// listing's — they are different listings and the counts differ.
func TestFieldsNamedOutOfRangeReportsTheSlotSize(t *testing.T) {
	service := newTestService(t, 10)
	if err := service.SaveNamed(State{Resources: namespaces("default", "prod"), Namespace: "default"}); err != nil {
		t.Fatalf("SaveNamed: %v", err)
	}
	save(t, service, State{Resources: pods("a", "b", "c", "d", "e"), Namespace: "default"})

	_, _, err := service.FieldsNamed(4, kinds.Namespace)
	if err == nil {
		t.Fatal("FieldsNamed(4) against a 2-entry slot succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "2 Namespaces") {
		t.Errorf("error = %q, want it to report '2 Namespaces'", err.Error())
	}
}

// The slot records the namespace its listing came from, so a context switch is
// captioned with the context it moved to.
func TestFieldsNamedReturnsTheSlotNamespace(t *testing.T) {
	service := newTestService(t, 10)
	if err := service.SaveNamed(State{
		Resources: NewResources([]string{"docker-desktop", "prod"}, kinds.Context),
		Namespace: "docker-desktop",
	}); err != nil {
		t.Fatalf("SaveNamed: %v", err)
	}

	_, namespace, err := service.FieldsNamed(2, kinds.Context)
	if err != nil {
		t.Fatalf("FieldsNamed: %v", err)
	}
	if namespace != "docker-desktop" {
		t.Errorf("namespace = %q, want %q", namespace, "docker-desktop")
	}
}

// A state file written before slots existed has no "named" key. It must keep
// loading and resolving exactly as it did.
func TestStateFileWithoutNamedKeyStillLoads(t *testing.T) {
	service := newTestService(t, 10)
	legacy := `{"version":2,"states":[{"resources":[{"name":"nginx","kind":"Pod"}],"namespace":"prod","query":null}],"cursor":0}`
	if err := os.WriteFile(service.Path, []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	name, namespace, kind, err := service.Fields(1)
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	if name != "nginx" || namespace != "prod" || kind != kinds.Pod {
		t.Errorf("Fields(1) = %q/%q/%q, want nginx/prod/Pod", name, namespace, kind)
	}
	if _, _, err := service.FieldsNamed(1, kinds.Namespace); err == nil {
		t.Error("FieldsNamed on a file with no slots succeeded, want an error")
	}
}

// A slot entry is decoded the way a stack entry is, so one with no "resources"
// key cannot become an empty listing that answers every index with a count of
// zero. It drops instead, and the empty-slot message names the command that
// refills it — where condemning the whole file would name `kx get`, which
// refills the stack and not the slot.
func TestSlotWithoutResourcesDropsAndKeepsTheHistory(t *testing.T) {
	service := newTestService(t, 10)
	raw := `{"version":2,"states":[{"resources":[{"name":"nginx","kind":"Pod"}],"namespace":"prod","query":null}],` +
		`"cursor":0,"named":{"Namespace":{"namespace":"prod"}}}`
	if err := os.WriteFile(service.Path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, _, err := service.FieldsNamed(1, kinds.Namespace)
	if err == nil {
		t.Fatal("FieldsNamed against a resource-less slot succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "No Namespaces listing yet") {
		t.Errorf("error = %q, want it to report an empty slot rather than a count", err)
	}
	if !strings.Contains(err.Error(), "kx ns") {
		t.Errorf("error = %q, want it to name the command that refills the slot", err)
	}

	// The stack is unrelated to the broken slot and must survive it.
	name, _, kind, err := service.Fields(1)
	if err != nil {
		t.Fatalf("Fields: %v — a bad slot took the history with it", err)
	}
	if name != "nginx" || kind != kinds.Pod {
		t.Errorf("Fields(1) = %q/%q, want nginx/Pod", kind, name)
	}
}

// A bad entry in the stack still condemns the file: there the unreadable error
// names `kx get <resource>`, which is exactly what rebuilds it.
func TestStackEntryWithoutResourcesIsStillFatal(t *testing.T) {
	service := newTestService(t, 10)
	raw := `{"version":2,"states":[{"namespace":"prod","query":null}],"cursor":0,` +
		`"named":{"Namespace":{"resources":[{"name":"prod","kind":"Namespace"}],"namespace":"prod"}}}`
	if err := os.WriteFile(service.Path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := service.Load()
	if err == nil {
		t.Fatal("Load succeeded on a stack entry with no resources")
	}
	if !strings.Contains(err.Error(), "kx get <resource>") {
		t.Errorf("error = %q, want it to name the recovery step", err)
	}
}

// The slot is an additive key: the history shape older versions read is
// untouched, so an upgrade is reversible.
func TestNamedSlotIsAnAdditiveKey(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("nginx"), Namespace: "prod"})
	if err := service.SaveNamed(State{Resources: namespaces("default"), Namespace: "prod"}); err != nil {
		t.Fatalf("SaveNamed: %v", err)
	}

	data, err := os.ReadFile(service.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, key := range []string{"states", "cursor", "named"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("state.json is missing the %q key", key)
		}
	}
	var named map[string]State
	if err := json.Unmarshal(raw["named"], &named); err != nil {
		t.Fatalf("Unmarshal named: %v", err)
	}
	if _, ok := named["Namespace"]; !ok {
		t.Errorf("named = %v, want a 'Namespace' slot", named)
	}
}

// Version 1 files predate both the per-resource namespace and the recorded
// context, so they reset rather than being read as entries that happen to
// carry neither — an unstamped context would read as "unknown" and wave every
// index through the cluster check it now exists to make.
func TestPreviousSchemaVersionResetsFile(t *testing.T) {
	service := newTestService(t, 10)
	v1 := `{"version":1,"states":[{"resources":[{"name":"nginx","kind":"Pod"}],"namespace":"prod"}],"cursor":0}`
	if err := os.WriteFile(service.Path, []byte(v1), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := service.Load(); err != ErrSchemaChanged {
		t.Fatalf("Load on a version 1 file = %v, want ErrSchemaChanged", err)
	}
	if _, err := service.Load(); err != ErrNoState {
		t.Errorf("Load after reset = %v, want ErrNoState", err)
	}
}

// A resource carries its own namespace so that a listing spanning several —
// which is what `kx get -A` produces — can still resolve an index to the one
// place the resource actually lives.
func TestPerResourceNamespaceRoundTrips(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: NewOrderedResources([]Resource{
		{Name: "api", Kind: kinds.Pod, Namespace: "prod"},
		{Name: "api", Kind: kinds.Pod, Namespace: "staging"},
	})})

	loaded, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	first, ok := loaded.Resources.At(1)
	if !ok || first.Namespace != "prod" {
		t.Errorf("At(1).Namespace = %q, want %q", first.Namespace, "prod")
	}
	second, ok := loaded.Resources.At(2)
	if !ok || second.Namespace != "staging" {
		t.Errorf("At(2).Namespace = %q, want %q", second.Namespace, "staging")
	}
}

// The entry-level namespace stays the answer for every listing that has one,
// so a single-namespace entry writes no per-resource namespace at all and its
// on-disk shape is unchanged.
func TestSingleNamespaceListingWritesNoPerResourceNamespace(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("nginx"), Namespace: "prod"})

	data, err := os.ReadFile(service.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(data), `"resources":[{"name":"nginx","kind":"Pod","namespace"`) {
		t.Errorf("a listing with no per-resource namespace wrote one anyway:\n%s", data)
	}
}

func TestFieldsPrefersThePerResourceNamespace(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{
		Resources: NewOrderedResources([]Resource{
			{Name: "api", Kind: kinds.Pod, Namespace: "prod"},
			{Name: "web", Kind: kinds.Pod, Namespace: "staging"},
		}),
		Namespace: "",
	})

	name, namespace, kind, err := service.Fields(2)
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	if name != "web" || namespace != "staging" || kind != kinds.Pod {
		t.Errorf("Fields(2) = %q, %q, %q; want web, staging, Pod", name, namespace, kind)
	}
}

// A resource with no namespace of its own falls back to the entry's, which is
// every listing kx saved before per-resource namespaces existed.
func TestFieldsFallsBackToTheEntryNamespace(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("nginx"), Namespace: "prod"})

	if _, namespace, _, err := service.Fields(1); err != nil || namespace != "prod" {
		t.Errorf("Fields(1) namespace = %q (err %v), want prod", namespace, err)
	}
}

func TestFieldsExpectingPrefersThePerResourceNamespace(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: NewOrderedResources([]Resource{
		{Name: "api", Kind: kinds.Pod, Namespace: "prod"},
	})})

	name, namespace, err := service.FieldsExpecting(1, kinds.Pod)
	if err != nil {
		t.Fatalf("FieldsExpecting: %v", err)
	}
	if name != "api" || namespace != "prod" {
		t.Errorf("FieldsExpecting(1, Pod) = %q, %q; want api, prod", name, namespace)
	}
}

func TestFieldsNamedPrefersThePerResourceNamespace(t *testing.T) {
	service := newTestService(t, 10)
	if err := service.SaveNamed(State{Resources: NewOrderedResources([]Resource{
		{Name: "kube-system", Kind: kinds.Namespace, Namespace: "kube-system"},
	})}); err != nil {
		t.Fatalf("SaveNamed: %v", err)
	}

	_, namespace, err := service.FieldsNamed(1, kinds.Namespace)
	if err != nil {
		t.Fatalf("FieldsNamed: %v", err)
	}
	if namespace != "kube-system" {
		t.Errorf("namespace = %q, want %q", namespace, "kube-system")
	}
}

// Every save stamps the context the listing was taken against, so that an
// index can later be checked against the cluster it was counted in. Stamped
// centrally rather than passed in by callers: the tree walk and the triage
// sweep both save state without holding a kubectl service.
func TestSaveStampsTheCurrentContext(t *testing.T) {
	service := newTestService(t, 10)
	service.Context = func() string { return "staging" }
	save(t, service, State{Resources: pods("nginx"), Namespace: "prod"})

	loaded, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Context != "staging" {
		t.Errorf("Context = %q, want %q", loaded.Context, "staging")
	}
}

func TestSaveNamedStampsTheCurrentContext(t *testing.T) {
	service := newTestService(t, 10)
	service.Context = func() string { return "staging" }
	if err := service.SaveNamed(State{Resources: namespaces("default")}); err != nil {
		t.Fatalf("SaveNamed: %v", err)
	}

	history, err := service.LoadHistory()
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if got := history.Named[kinds.Namespace].Context; got != "staging" {
		t.Errorf("slot Context = %q, want %q", got, "staging")
	}
}

// A context the caller set explicitly wins over the hook, so a replayed or
// reconstructed entry keeps the context it was actually listed in.
func TestSaveKeepsAnAlreadyRecordedContext(t *testing.T) {
	service := newTestService(t, 10)
	service.Context = func() string { return "staging" }
	save(t, service, State{Resources: pods("nginx"), Context: "prod"})

	loaded, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Context != "prod" {
		t.Errorf("Context = %q, want %q (the caller's, not the hook's)", loaded.Context, "prod")
	}
}

// No hook — every test that builds a Service literally, and any build where
// the kubeconfig names no current context — stamps nothing rather than
// panicking or inventing one.
func TestSaveWithNoContextHookStampsNothing(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("nginx"), Namespace: "prod"})

	loaded, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Context != "" {
		t.Errorf("Context = %q, want empty", loaded.Context)
	}
}

// listedIn seeds an entry recorded against one context, then leaves the service
// reporting another — the shape of `kx get pods` followed by `kx context 2`.
func listedIn(t *testing.T, listed, current string, entry State) *Service {
	t.Helper()
	service := newTestService(t, 10)
	service.Context = func() string { return listed }
	if err := service.Save(entry); err != nil {
		t.Fatalf("Save: %v", err)
	}
	service.Context = func() string { return current }
	return service
}

// An index counted in one cluster must not be spent in another: names repeat
// across clusters, so the resource it resolves to is a different resource that
// happens to share a name.
func TestFieldsRefusesAnIndexFromAnotherContext(t *testing.T) {
	service := listedIn(t, "staging", "production",
		State{Resources: pods("api"), Namespace: "prod"})

	_, _, _, err := service.Fields(1)

	var mismatch ContextMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("Fields = %v, want a ContextMismatchError", err)
	}
	if mismatch.Listed != "staging" || mismatch.Current != "production" {
		t.Errorf("mismatch = %+v, want listed staging, current production", mismatch)
	}
}

func TestContextMismatchErrorNamesBothContexts(t *testing.T) {
	err := ContextMismatchError{Index: 3, Listed: "staging", Current: "production"}

	for _, want := range []string{"3", "staging", "production"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Error() = %q, want it to name %q", err.Error(), want)
		}
	}
}

func TestFieldsAllowsAMatchingContext(t *testing.T) {
	service := listedIn(t, "staging", "staging",
		State{Resources: pods("api"), Namespace: "prod"})

	if _, _, _, err := service.Fields(1); err != nil {
		t.Errorf("Fields on a matching context = %v, want nil", err)
	}
}

// An entry with no recorded context is from a kubeconfig that names no current
// one. Unknown is not a mismatch — blocking on it would break kx for anyone
// whose kubeconfig has no current-context set.
func TestFieldsAllowsAnEntryWithNoRecordedContext(t *testing.T) {
	service := listedIn(t, "", "production",
		State{Resources: pods("api"), Namespace: "prod"})

	if _, _, _, err := service.Fields(1); err != nil {
		t.Errorf("Fields on an unrecorded context = %v, want nil", err)
	}
}

func TestFieldsAllowsAnUnknownCurrentContext(t *testing.T) {
	service := listedIn(t, "staging", "",
		State{Resources: pods("api"), Namespace: "prod"})

	if _, _, _, err := service.Fields(1); err != nil {
		t.Errorf("Fields with no current context = %v, want nil", err)
	}
}

func TestFieldsExpectingRefusesAnIndexFromAnotherContext(t *testing.T) {
	service := listedIn(t, "staging", "production",
		State{Resources: pods("api"), Namespace: "prod"})

	_, _, err := service.FieldsExpecting(1, kinds.Pod)

	var mismatch ContextMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("FieldsExpecting = %v, want a ContextMismatchError", err)
	}
}

// The namespace slot is as cluster-bound as any listing: namespaces are server
// objects, and the ones in another cluster are different namespaces.
func TestFieldsNamedRefusesForTheNamespaceSlot(t *testing.T) {
	service := newTestService(t, 10)
	service.Context = func() string { return "staging" }
	if err := service.SaveNamed(State{Resources: namespaces("default", "kube-system")}); err != nil {
		t.Fatalf("SaveNamed: %v", err)
	}
	service.Context = func() string { return "production" }

	_, _, err := service.FieldsNamed(2, kinds.Namespace)

	var mismatch ContextMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("FieldsNamed on the namespace slot = %v, want a ContextMismatchError", err)
	}
	// A slot has no query to replay, so it must carry its own relist command —
	// without it the caller falls back to replaying the history stack, which
	// answers a namespace question with whatever was last listed.
	if mismatch.Relist != "kx ns" {
		t.Errorf("Relist = %q, want %q", mismatch.Relist, "kx ns")
	}
}

// A history entry carries the query that built it, so the caller replays that
// rather than advising a command. An entry that advised one would be telling
// the user to do what kx is about to do for them.
func TestStackMismatchCarriesNoRelistHint(t *testing.T) {
	service := listedIn(t, "staging", "production",
		State{Resources: pods("api"), Namespace: "prod"})

	_, _, _, err := service.Fields(1)

	var mismatch ContextMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("Fields = %v, want a ContextMismatchError", err)
	}
	if mismatch.Relist != "" {
		t.Errorf("Relist = %q, want empty so the caller replays the saved query", mismatch.Relist)
	}
}

// The context slot is the one exemption, and it is not a special case so much
// as the point: contexts live in kubeconfig, not in a cluster, and the slot
// exists to switch between them. Guarding it would make `kx context 2`
// unusable the moment it had been used once — the switch it performs is what
// creates the mismatch that would then block the next switch.
func TestFieldsNamedAllowsTheContextSlot(t *testing.T) {
	service := newTestService(t, 10)
	service.Context = func() string { return "staging" }
	if err := service.SaveNamed(State{
		Resources: NewResources([]string{"staging", "production"}, kinds.Context),
	}); err != nil {
		t.Fatalf("SaveNamed: %v", err)
	}
	service.Context = func() string { return "production" }

	name, _, err := service.FieldsNamed(1, kinds.Context)
	if err != nil {
		t.Fatalf("FieldsNamed on the context slot = %v, want it to resolve", err)
	}
	if name != "staging" {
		t.Errorf("name = %q, want %q — switching back must stay possible", name, "staging")
	}
}

// An all-namespace listing has no entry-level namespace by design. Defaulting
// that empty value to "default" — which every stack entry used to get, back
// when an empty namespace could only mean "unrecorded" — captions an
// all-namespace listing with a namespace it never came from, and then resolves
// every one of its indexes into that namespace.
func TestSpanningEntryKeepsItsEmptyNamespace(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{AllNamespaces: true, Resources: NewOrderedResources([]Resource{
		{Name: "api", Kind: kinds.Pod, Namespace: "prod"},
		{Name: "api", Kind: kinds.Pod, Namespace: "staging"},
	})})

	loaded, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Namespace != "" {
		t.Errorf("Namespace = %q, want empty — the listing spans namespaces", loaded.Namespace)
	}
}

// An all-namespace listing whose table carried no NAMESPACE column records no
// namespaces at all — `kx get pods -A -o custom-columns=NAME:.metadata.name`
// is the shape. Read back as "default" it looked like an ordinary listing from
// that namespace, so every index resolved into it.
func TestAllNamespacesEntryWithNoRecordedNamespacesKeepsItsEmptyNamespace(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{AllNamespaces: true, Resources: pods("nginx", "api")})

	loaded, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Namespace != "" {
		t.Errorf("Namespace = %q, want empty — the listing spans namespaces", loaded.Namespace)
	}
}

// An entry recording no namespace anywhere is genuinely unscoped, and still
// reads back as "default" so nothing downstream has to handle an empty one.
func TestUnscopedEntryStillDefaults(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("nginx")})

	loaded, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Namespace != "default" {
		t.Errorf("Namespace = %q, want default", loaded.Namespace)
	}
}

// An entry with no namespace used to be "corrected" to "default" on load, so
// a cluster-scoped listing that deliberately recorded none got one invented
// for it — kx get nodes then described Node/x as living in "default".
//
// The scope is recovered from the kinds the entry already records rather than
// from a new field: every resource carries its Kind, and a listing whose sole
// kind is cluster-scoped has no namespace by definition.
func TestClusterScopedEntryKeepsItsEmptyNamespace(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{
		Resources: NewResources([]string{"node-a"}, kinds.Node),
		Namespace: "",
	})

	loaded, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Namespace != "" {
		t.Errorf("Namespace = %q, want empty — a Node listing has no namespace", loaded.Namespace)
	}

	_, namespace, kind, err := service.Fields(1)
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	if namespace != "" {
		t.Errorf("Fields namespace = %q, want empty", namespace)
	}
	if kind != kinds.Node {
		t.Errorf("Fields kind = %q, want Node", kind)
	}
}

// The backfill still applies to a namespaced listing that recorded none, which
// is what it was written for.
func TestNamespacedEntryWithNoNamespaceStillDefaults(t *testing.T) {
	service := newTestService(t, 10)
	save(t, service, State{Resources: pods("nginx"), Namespace: ""})

	loaded, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Namespace != "default" {
		t.Errorf("Namespace = %q, want default", loaded.Namespace)
	}
}

// A mixed-kind entry — a tree walk, a triage sweep — has no sole kind to read
// a scope from, so it keeps the backfill rather than having one guessed.
func TestMixedKindEntryStillDefaults(t *testing.T) {
	service := newTestService(t, 10)
	mixed := NewOrderedResources([]Resource{
		{Name: "web", Kind: kinds.Deployment},
		{Name: "web-abc", Kind: kinds.Pod},
	})
	save(t, service, State{Resources: mixed, Namespace: ""})

	loaded, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Namespace != "default" {
		t.Errorf("Namespace = %q, want default for a mixed-kind entry", loaded.Namespace)
	}
}

// countingSource records how often the discovery fallback was consulted, and
// answers "don't know" — the shape a cache that has never heard of a kind has.
type countingSource struct{ namespacedCalls int }

func (c *countingSource) Resolve(string) (kinds.Kind, string, bool) { return "", "", false }
func (c *countingSource) Namespaced(kinds.Kind) (bool, bool) {
	c.namespacedCalls++
	return false, false
}

// The namespace backfill asks kinds.Namespaced, which falls through to the
// discovery cache for any kind outside kx's static tables — every CRD, and a
// stale cache means network round-trips per API group.
//
// It used to run over every entry in the stack on every load, and Load runs on
// every index-resolving command. So one listing of a cluster-scoped custom
// resource anywhere in history made `kx describe 1` against a pod listing wait
// on discovery before it could reach kubectl.
func TestLoadDoesNotConsultDiscoveryForOtherHistoryEntries(t *testing.T) {
	service := &Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")}
	// Written straight to disk rather than through Save, which loads the stack
	// and writes it back: that round trip would persist whatever the backfill
	// decided about the CRD entry, and the assertion below would then pass for
	// the wrong reason on either implementation.
	if err := service.saveHistory(History{
		Cursor: 1,
		States: []State{
			// A CRD listing with no namespace: the entry that reaches the
			// discovery fallback.
			{Resources: NewResources([]string{"web-gateway"}, kinds.Kind("Gateway"))},
			// The pod listing on top, which is what the cursor points at.
			{Resources: NewResources([]string{"nginx"}, kinds.Pod), Namespace: "prod"},
		},
	}); err != nil {
		t.Fatalf("write the stack: %v", err)
	}

	source := &countingSource{}
	kinds.SetShorthandSource(source)
	t.Cleanup(func() { kinds.SetShorthandSource(nil) })

	current, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if current.Namespace != "prod" {
		t.Errorf("namespace = %q, want prod", current.Namespace)
	}
	if source.namespacedCalls != 0 {
		t.Errorf("Load consulted discovery %d time(s) for an entry it did not return",
			source.namespacedCalls)
	}
}

// The entry being read still gets the backfill, so the saving above is in what
// is skipped rather than in the answer.
func TestLoadStillBackfillsTheEntryItReturns(t *testing.T) {
	service := &Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")}
	if err := service.Save(State{
		Resources: NewResources([]string{"nginx"}, kinds.Pod),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	current, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if current.Namespace != "default" {
		t.Errorf("namespace = %q, want the backfilled default", current.Namespace)
	}
}

// A cluster-scoped listing keeps its empty namespace — #271, which the
// backfill has to keep honouring wherever it now runs.
func TestLoadLeavesAClusterScopedListingWithoutANamespace(t *testing.T) {
	service := &Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")}
	if err := service.Save(State{
		Resources: NewResources([]string{"node-a"}, kinds.Node),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	current, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if current.Namespace != "" {
		t.Errorf("namespace = %q, want empty — a Node is not in a namespace", current.Namespace)
	}
}
