package state

import (
	"encoding/json"
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

// The on-disk shape is a compatibility surface, not an implementation detail:
// every installed kx reads whatever ~/.kx/state.json is already there, including
// files written by older versions. Changing these keys silently breaks upgrades.
func TestSavedJSONMatchesOnDiskSchema(t *testing.T) {
	service := newTestService(t, 10)
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
		States []struct {
			Resources map[string]string `json:"resources"`
			Namespace string            `json:"namespace"`
			Query     *struct {
				Resource string   `json:"resource"`
				Args     []string `json:"args"`
				Match    *string  `json:"match"`
			} `json:"query"`
		} `json:"states"`
		Cursor int `json:"cursor"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("saved state is not the expected schema: %v\n%s", err, data)
	}
	if len(raw.States) != 1 || raw.States[0].Resources["nginx"] != "Pod" {
		t.Errorf("resources = %v, want {nginx: Pod}", raw.States[0].Resources)
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

// A file written before history existed is a bare state object with no
// "states" key.
func TestLegacySingleStateFormatLoads(t *testing.T) {
	service := newTestService(t, 10)
	legacy := `{"resources": {"nginx": "Pod"}, "namespace": "prod"}`
	if err := os.WriteFile(service.Path, []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	loaded, err := service.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Namespace != "prod" || len(loaded.Names()) != 1 {
		t.Errorf("legacy load = %+v", loaded)
	}
	if loaded.Query != nil {
		t.Errorf("Query = %+v, want nil for a pre-query state file", loaded.Query)
	}
}

func TestCorruptFileReturnsActionableError(t *testing.T) {
	for name, contents := range map[string]string{
		"invalid json": "{not json",
		"missing keys": `{"states": [{"namespace": "prod"}], "cursor": 0}`,
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
	legacy := `{"states":[{"resources":{"nginx":"Pod"},"namespace":"prod","query":null}],"cursor":0}`
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
