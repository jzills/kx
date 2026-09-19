package cli

import (
	"bytes"
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
