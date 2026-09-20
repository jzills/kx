package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
)

// kx mark api 3 pins what index 3 resolves to right now, with the context it
// was taken in.
func TestMarkPinsTheResolvedResource(t *testing.T) {
	services := switchServices(t, &recordingKubectl{})
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"api-7d8f"}, kinds.Pod), Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cmd := newMarkCommand(services)
	cmd.SetArgs([]string{"api", "1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx mark api 1: %v", err)
	}

	marks, err := services.State.Marks()
	if err != nil {
		t.Fatalf("Marks: %v", err)
	}
	mark, ok := marks["api"]
	if !ok {
		t.Fatalf("marks = %+v, want an entry for api", marks)
	}
	if mark.Name != "api-7d8f" || mark.Namespace != "prod" || mark.Kind != kinds.Pod {
		t.Errorf("mark = %+v, want api-7d8f/prod/Pod", mark)
	}
	if mark.Context == "" {
		t.Error("mark recorded no context; a mark must not be portable between clusters")
	}
}

// A bad index must refuse before anything is stored. TestMarkRefusesANumericName
// cannot pin this: it fails on name validation before resolveRefs is ever
// reached, so it would still pass if SaveMark ran ahead of resolveRefs. This
// test marks against an out-of-range index — the name is valid, so the only
// thing that can stop it is the resolve — and checks both that it errors and
// that no mark was left behind.
func TestMarkRefusesABadIndexBeforeStoringAnything(t *testing.T) {
	services := switchServices(t, &recordingKubectl{})
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"api"}, kinds.Pod), Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cmd := newMarkCommand(services)
	cmd.SetArgs([]string{"api", "99"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("kx mark api 99 succeeded; index 99 is out of range")
	}

	marks, err := services.State.Marks()
	if err != nil {
		t.Fatalf("Marks: %v", err)
	}
	if len(marks) != 0 {
		t.Errorf("marks = %+v, want none — a bad index must refuse before anything is stored", marks)
	}
}

func TestMarkRefusesANumericName(t *testing.T) {
	services := switchServices(t, &recordingKubectl{})
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"api"}, kinds.Pod), Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cmd := newMarkCommand(services)
	cmd.SetArgs([]string{"3", "1"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("kx mark 3 1 succeeded; a numeric mark name is ambiguous with an index")
	}
}

// The mark listing prints names with their sigil ("@api"), and copying what
// is on screen is the obvious way to spend one — kx unmark must accept it
// rather than reporting an unknown mark literally named "@api".
func TestUnmarkAcceptsTheSigilItsOwnListingPrints(t *testing.T) {
	services := switchServices(t, &recordingKubectl{})
	if err := services.State.SaveMark("api", state.Mark{
		Resource: state.Resource{Name: "api", Kind: kinds.Pod, Namespace: "prod"},
	}); err != nil {
		t.Fatalf("SaveMark: %v", err)
	}

	cmd := newUnmarkCommand(services)
	cmd.SetArgs([]string{"@api"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx unmark @api: %v", err)
	}

	marks, _ := services.State.Marks()
	if len(marks) != 0 {
		t.Errorf("marks = %+v, want none", marks)
	}
}

// A name that survives the sigil strip but could never have been stored is
// reported for what it is. DropMark's "No mark named" error interpolates the
// name into a 'kx mark %s <index>' suggestion, so a bare "@" recommended a
// command with a hole where the name goes, and a doubled "@@web" recommended
// one validMarkName itself rejects.
func TestUnmarkRefusesANameThatCouldNeverHaveBeenStored(t *testing.T) {
	for _, arg := range []string{"@", "@@web"} {
		services := switchServices(t, &recordingKubectl{})
		// A real mark has to exist, or DropMark reports ErrNoState before it
		// ever reaches the "No mark named" branch this guards — and the test
		// would pass whether or not the name was validated.
		if err := services.State.SaveMark("web", state.Mark{
			Resource: state.Resource{Name: "web-abc", Kind: kinds.Pod, Namespace: "prod"},
		}); err != nil {
			t.Fatalf("SaveMark: %v", err)
		}
		cmd := newUnmarkCommand(services)
		cmd.SetArgs([]string{arg})
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("kx unmark %s succeeded, want a refusal", arg)
		}
		if strings.Contains(err.Error(), "kx mark  <index>") {
			t.Errorf("kx unmark %s suggests a command with no name in it: %q", arg, err)
		}
		if strings.Contains(err.Error(), "kx mark @") {
			t.Errorf("kx unmark %s suggests a name kx would refuse to create: %q", arg, err)
		}
	}
}

func TestUnmarkRemovesOne(t *testing.T) {
	services := switchServices(t, &recordingKubectl{})
	if err := services.State.SaveMark("api", state.Mark{
		Resource: state.Resource{Name: "api", Kind: kinds.Pod, Namespace: "prod"},
	}); err != nil {
		t.Fatalf("SaveMark: %v", err)
	}

	cmd := newUnmarkCommand(services)
	cmd.SetArgs([]string{"api"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx unmark api: %v", err)
	}

	marks, _ := services.State.Marks()
	if len(marks) != 0 {
		t.Errorf("marks = %+v, want none", marks)
	}
}

// kx mark with no arguments lists, and saves no state — marks are spent by
// name, so numbering them would invite `kx mark 2` to mean something.
func TestMarkWithNoArgumentsListsAndSavesNothing(t *testing.T) {
	services := switchServices(t, &recordingKubectl{})
	if err := services.State.SaveMark("api", state.Mark{
		Resource: state.Resource{Name: "api-7d8f", Kind: kinds.Pod, Namespace: "prod"},
	}); err != nil {
		t.Fatalf("SaveMark: %v", err)
	}
	before, _ := services.State.LoadHistory()

	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")
	cmd := newMarkCommand(services)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx mark: %v", err)
	}

	if !strings.Contains(out.String(), "api-7d8f") {
		t.Errorf("output = %q, want the marked resource listed", out.String())
	}
	after, _ := services.State.LoadHistory()
	if len(after.States) != len(before.States) {
		t.Error("kx mark pushed a history entry; listing marks must not disturb the stack")
	}
}

// --all is destructive, so it confirms first — mirroring TestDropAllConfirmsBeforeClearing.
func TestUnmarkAllConfirmsBeforeClearing(t *testing.T) {
	services := switchServices(t, &recordingKubectl{})
	for _, name := range []string{"api", "web"} {
		if err := services.State.SaveMark(name, state.Mark{
			Resource: state.Resource{Name: name, Kind: kinds.Pod, Namespace: "prod"},
		}); err != nil {
			t.Fatalf("SaveMark(%s): %v", name, err)
		}
	}

	var prompted string
	services.Confirm = func(m string) error { prompted = m; return nil }

	cmd := newUnmarkCommand(services)
	cmd.SetArgs([]string{"--all"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx unmark --all: %v", err)
	}
	if prompted == "" {
		t.Error("kx unmark --all did not prompt for confirmation")
	}

	marks, err := services.State.Marks()
	if err != nil {
		t.Fatalf("Marks: %v", err)
	}
	if len(marks) != 0 {
		t.Errorf("marks = %+v, want none after unmark --all", marks)
	}
}

// Declining the prompt must remove nothing.
func TestUnmarkAllAbortsWithoutConfirmation(t *testing.T) {
	services := switchServices(t, &recordingKubectl{})
	if err := services.State.SaveMark("api", state.Mark{
		Resource: state.Resource{Name: "api", Kind: kinds.Pod, Namespace: "prod"},
	}); err != nil {
		t.Fatalf("SaveMark: %v", err)
	}
	services.Confirm = func(string) error { return errors.New("aborted") }

	cmd := newUnmarkCommand(services)
	cmd.SetArgs([]string{"--all"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("kx unmark --all succeeded despite an aborted confirmation")
	}

	marks, err := services.State.Marks()
	if err != nil {
		t.Fatalf("Marks: %v", err)
	}
	if len(marks) != 1 {
		t.Errorf("marks = %+v, want the mark still present", marks)
	}
}

// kx mark and kx unmark set no SuggestFor unlike most siblings, so a typed
// plural fell through to `kx get marks`/`kx get unmarks` and a kubectl error
// instead of being offered the right command.
// Driven through Execute rather than SuggestionsFor, because cobra defaults
// SuggestionsMinimumDistance to 2 inside Execute and leaves it 0 on the struct
// — so calling SuggestionsFor directly reports what no user ever sees, and an
// explicit SuggestFor entry would make such a test pass while masking that the
// distance match already covers a plural.
func TestMarkAndUnmarkSuggestThePluralTypoExactlyOnce(t *testing.T) {
	for typo, want := range map[string]string{"marks": "mark", "unmarks": "unmark"} {
		root := NewRoot(Services{}, "test")
		root.SetArgs([]string{typo})
		err := root.Execute()
		if err == nil {
			t.Fatalf("kx %s succeeded, want an unknown-command error", typo)
		}
		// Counted by suggestion line, not by substring: the typo itself contains
		// the command's name. Listing SuggestFor alongside cobra's own distance
		// match printed the same name twice under one "Did you mean this?".
		suggested := 0
		for _, line := range strings.Split(err.Error(), "\n") {
			if strings.TrimSpace(line) == want {
				suggested++
			}
		}
		if suggested != 1 {
			t.Errorf("kx %s suggested %q on %d lines, want exactly 1:\n%s",
				typo, want, suggested, err)
		}
	}
}

// kx mark resolves an index exactly the way kx describe does, so a context
// mismatch on it must recover the same way — registered with withoutRefresh,
// `kx get pods` in staging followed by `kx context 2` then `kx mark api 3`
// used to dead-end on a bare ContextMismatchError where `kx describe 3` in
// the identical state relisted and offered fresh indexes to pick from.
//
// Run through NewRoot rather than by wrapping newMarkCommand directly, so
// this actually exercises root.go's registration — the thing item 2 changes —
// rather than only the withRefresh/isStale mechanism in isolation, which
// would stay green even if root.go still registered kx mark with
// withoutRefresh.
func TestMarkRecoversFromAContextMismatchLikeDescribe(t *testing.T) {
	out := captureRender(t)
	services := mismatchServices(t, &fakeKubectl{output: podsOutput},
		&state.Query{Resource: "pods", Args: []string{}})

	root := NewRoot(services, "test")
	root.SetArgs([]string{"mark", "api", "1"})
	err := root.Execute()
	if err == nil {
		t.Fatal("kx mark api 1 succeeded across a context mismatch")
	}

	var silent SilentError
	if !errors.As(err, &silent) {
		t.Fatalf("err = %v, want SilentError — the refresh already reported it", err)
	}
	if !strings.Contains(out.String(), "nginx-abc-xyz") {
		t.Errorf("output = %q, want the refreshed listing to mark from", out.String())
	}

	marks, merr := services.State.Marks()
	if merr != nil {
		t.Fatalf("Marks: %v", merr)
	}
	if len(marks) != 0 {
		t.Errorf("marks = %+v, want none — the mismatch must refuse before storing anything", marks)
	}
}

// --all clears every mark by name; combining it with a name is a different,
// unsupported request rather than a filter on which mark --all removes.
func TestUnmarkAllTakesNoNameArgument(t *testing.T) {
	services := switchServices(t, &recordingKubectl{})
	if err := services.State.SaveMark("api", state.Mark{
		Resource: state.Resource{Name: "api", Kind: kinds.Pod, Namespace: "prod"},
	}); err != nil {
		t.Fatalf("SaveMark: %v", err)
	}
	services.Confirm = func(string) error { return nil }

	cmd := newUnmarkCommand(services)
	cmd.SetArgs([]string{"--all", "api"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("kx unmark --all api succeeded; --all takes no name argument")
	}

	marks, err := services.State.Marks()
	if err != nil {
		t.Fatalf("Marks: %v", err)
	}
	if len(marks) != 1 {
		t.Errorf("marks = %+v, want the mark untouched by the refusal", marks)
	}
}
