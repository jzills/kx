package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
)

// The leading run of numbers are indexes; everything after is kubectl's.
func TestSplitLeadingIndexes(t *testing.T) {
	cases := []struct {
		args        []string
		wantIndexes int
		wantRest    string
	}{
		{[]string{"1", "2", "3"}, 3, ""},
		{[]string{"1", "--show-events=false"}, 1, "--show-events=false"},
		{[]string{"1", "2", "-o", "wide"}, 2, "-o wide"},
		{[]string{"--flag", "1"}, 0, "--flag 1"},
		{nil, 0, ""},
	}
	for _, tc := range cases {
		indexes, rest := splitLeadingIndexes(tc.args)
		if len(indexes) != tc.wantIndexes {
			t.Errorf("splitLeadingIndexes(%v) indexes = %v, want %d", tc.args, indexes, tc.wantIndexes)
		}
		if joinArgs(rest) != tc.wantRest {
			t.Errorf("splitLeadingIndexes(%v) rest = %q, want %q", tc.args, joinArgs(rest), tc.wantRest)
		}
	}
}

func TestSplitAtDoubleDash(t *testing.T) {
	before, after := splitAtDoubleDash([]string{"1", "-c", "app", "--", "ls", "/x"})
	if joinArgs(before) != "1 -c app" {
		t.Errorf("before = %q", joinArgs(before))
	}
	if joinArgs(after) != "ls /x" {
		t.Errorf("after = %q", joinArgs(after))
	}

	before, after = splitAtDoubleDash([]string{"1"})
	if joinArgs(before) != "1" || after != nil {
		t.Errorf("no separator: before = %q, after = %v", joinArgs(before), after)
	}
}

func TestParseIndexNamesTheArgument(t *testing.T) {
	_, err := parseIndex("indexes", "abc")
	if err == nil {
		t.Fatal("parseIndex accepted a non-integer")
	}
	want := "Invalid value for 'indexes': 'abc' is not a valid int."
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

// Following several pods in turn would block on the first and never reach the
// rest, so it is refused rather than half-done.
func TestCheckFollow(t *testing.T) {
	cases := []struct {
		args    []string
		indexes int
		wantErr bool
	}{
		{[]string{"-f"}, 1, false},
		{[]string{"--follow"}, 1, false},
		{[]string{"-f"}, 2, true},
		{[]string{"--follow"}, 3, true},
		{[]string{"--tail=10"}, 3, false},
		{nil, 3, false},
	}
	for _, tc := range cases {
		err := checkFollow(tc.args, tc.indexes)
		if (err != nil) != tc.wantErr {
			t.Errorf("checkFollow(%v, %d) = %v, wantErr %v", tc.args, tc.indexes, err, tc.wantErr)
		}
	}
}

// delete, yaml, describe, events, labels and annotations all take several
// indexes in the Python CLI. Porting any of them as single-index is silent —
// cobra rejects the second argument with a generic arity error — so the arity
// is asserted here rather than left to a live comparison.
func TestMultiIndexCommandsAcceptSeveral(t *testing.T) {
	services := Services{}
	commands := map[string]*cobra.Command{
		"delete":      newDeleteCommand(services),
		"yaml":        newYamlCommand(services),
		"describe":    newDescribeCommand(services),
		"events":      newEventsCommand(services),
		"labels":      newMetadataReadCommand(services, "labels", "", "labels", "LABEL", true),
		"annotations": newMetadataReadCommand(services, "annotations", "", "annotations", "ANNOTATION", false),
		"logs":        newLogsCommand(services),
	}
	for name, cmd := range commands {
		if cmd.Args == nil {
			continue
		}
		if err := cmd.Args(cmd, []string{"1", "2"}); err != nil {
			t.Errorf("%s rejects two indexes: %v", name, err)
		}
		if !strings.Contains(cmd.Use, "...") {
			t.Errorf("%s: Use = %q, want it to show a repeatable index", name, cmd.Use)
		}
	}
}

// switchServices wires the real state service against a temp file, so the
// listing path and the switch path meet the way they do in the tool.
func switchServices(t *testing.T, kube kubectl.Service) Services {
	t.Helper()
	// The package renderer is nil until configured, and these tests drive the
	// listing path, which renders.
	render.Configure("default", true)
	store := &state.Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")}
	return Services{
		Kubectl: kube, State: store, Index: index.Service{}, Config: config.Default(),
	}
}

const namespaceTable = "NAME      STATUS   AGE\n" +
	"default   Active   91d\n" +
	"prod      Active   91d\n"

// The whole of #156, through the real call path: list namespaces, list
// something else on top, then switch. The 2 counts against namespaces.
func TestNamespaceListThenSwitchIgnoresAnInterveningListing(t *testing.T) {
	kube := &recordingKubectl{output: namespaceTable}
	services := switchServices(t, kube)

	if err := listSwitchTargets(services, false); err != nil {
		t.Fatalf("listSwitchTargets: %v", err)
	}
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"nginx", "redis"}, kinds.Pod),
		Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save pods: %v", err)
	}

	name, err := SwitchCommand{Kubectl: kube, State: services.State}.namespace(2)
	if err != nil {
		t.Fatalf("namespace(2): %v", err)
	}
	if name != "prod" {
		t.Errorf("namespace(2) = %q, want prod — resolved against the wrong listing", name)
	}
}

// `kx ns` is a switch listing, so it leaves the history stack alone. This is the
// churn half of #156: namespace listings used to crowd out the work between them.
func TestNamespaceListingDoesNotTouchHistory(t *testing.T) {
	kube := &recordingKubectl{output: namespaceTable}
	services := switchServices(t, kube)

	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"nginx"}, kinds.Pod),
		Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save pods: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := listSwitchTargets(services, false); err != nil {
			t.Fatalf("listSwitchTargets: %v", err)
		}
	}

	history, err := services.State.LoadHistory()
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(history.States) != 1 {
		t.Errorf("history has %d entries, want 1 — `kx ns` pushed onto the stack", len(history.States))
	}
	current, err := services.State.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := current.Names(); len(got) != 1 || got[0] != "nginx" {
		t.Errorf("current entry = %v, want [nginx] — the pods listing was displaced", got)
	}
}

// `kx get ns` is the escape hatch: it behaves like any other listing, so
// `kx describe <n>` still works on a namespace, and it refreshes the slot too.
func TestGetNamespacesPopulatesHistoryAndSlot(t *testing.T) {
	kube := &recordingKubectl{output: namespaceTable}
	services := switchServices(t, kube)

	if err := runGet(services, "namespaces", nil, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}

	name, _, kind, err := services.State.Fields(2)
	if err != nil {
		t.Fatalf("Fields(2): %v", err)
	}
	if name != "prod" || kind != kinds.Namespace {
		t.Errorf("Fields(2) = %s/%s, want Namespace/prod", kind, name)
	}
	slotName, _, err := services.State.FieldsNamed(2, kinds.Namespace)
	if err != nil {
		t.Fatalf("FieldsNamed(2): %v", err)
	}
	if slotName != "prod" {
		t.Errorf("FieldsNamed(2) = %q, want prod", slotName)
	}
}

// Contexts no longer land in history, so the caption cannot come from the
// current entry: on a fresh install there is none, and otherwise it would name
// whatever resource listing happened to be there.
func TestContextListingCaptionsWithoutHistory(t *testing.T) {
	kube := &recordingKubectl{
		output: "CURRENT   NAME             CLUSTER\n*         docker-desktop   docker-desktop\n",
	}
	services := switchServices(t, kube)

	if err := listSwitchTargets(services, true); err != nil {
		t.Fatalf("listSwitchTargets on empty history: %v", err)
	}
}
