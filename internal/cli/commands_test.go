package cli

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
)

// The leading run of numbers are indexes; everything after is kubectl's. A
// range token counts as a single leading argument here — it's expanded later,
// in parseIndexes.
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
		{[]string{"9..17", "-o", "wide"}, 1, "-o wide"},
		{[]string{"9..17", "3", "--tail=5"}, 2, "--tail=5"},
		// A malformed range still belongs to the leading run — it should reach
		// parseIndexes for a proper "not a valid range" error, rather than
		// falling through to the generic "not a valid int" message that
		// applies when the leading run is empty.
		{[]string{"5..", "-o", "wide"}, 1, "-o wide"},
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

// A range token expands into every index it spans, inclusive of both ends,
// walking in whichever direction the two ends imply.
func TestParseIndexesExpandsRanges(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []int
	}{
		{"ascending", []string{"9..12"}, []int{9, 10, 11, 12}},
		{"descending", []string{"12..9"}, []int{12, 11, 10, 9}},
		{"single value", []string{"5..5"}, []int{5}},
		{"mixed with literal indexes", []string{"1", "3..5", "7"}, []int{1, 3, 4, 5, 7}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseIndexes(fakeResolver{}, "indexes", tc.args)
			if err != nil {
				t.Fatalf("parseIndexes(%v) error = %v", tc.args, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseIndexes(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// A malformed range is reported the same way a bad single index is: named,
// quoted, and rejected before reaching the cluster.
func TestParseIndexesRejectsMalformedRanges(t *testing.T) {
	cases := []struct {
		arg  string
		want string
	}{
		{"9..abc", "Invalid value for 'indexes': '9..abc' is not a valid range."},
		{"9...17", "Invalid value for 'indexes': '9...17' is not a valid range."},
	}
	for _, tc := range cases {
		t.Run(tc.arg, func(t *testing.T) {
			_, err := parseIndexes(fakeResolver{}, "indexes", []string{tc.arg})
			if err == nil {
				t.Fatalf("parseIndexes(%q) accepted a malformed range", tc.arg)
			}
			if err.Error() != tc.want {
				t.Errorf("error = %q, want %q", err, tc.want)
			}
		})
	}
}

// "..5" defaults the start to 1; "5.." defaults the end to the size of the
// current listing, reported by the resolver's Count().
func TestParseIndexesExpandsOpenRanges(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		count int
		want  []int
	}{
		{"open start", []string{"..5"}, 0, []int{1, 2, 3, 4, 5}},
		{"open end", []string{"5.."}, 8, []int{5, 6, 7, 8}},
		{"open end, single item", []string{"5.."}, 5, []int{5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := fakeResolver{count: tc.count}
			got, err := parseIndexes(resolver, "indexes", tc.args)
			if err != nil {
				t.Fatalf("parseIndexes(%v) error = %v", tc.args, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseIndexes(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// A bare ".." is not "everything" — it's rejected the same way a malformed
// range is, so a destructive command like delete never expands an
// unqualified ".." into the whole listing by accident.
func TestParseIndexesRejectsBareDoubleDot(t *testing.T) {
	_, err := parseIndexes(fakeResolver{}, "indexes", []string{".."})
	if err == nil {
		t.Fatal("parseIndexes accepted a bare '..'")
	}
	want := "Invalid value for 'indexes': '..' is not a valid range."
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

// An open-end range whose start is already past the current listing is a
// hard error — not a silent reverse-walk (unlike an explicit "20..9") and
// not a silent no-op.
func TestParseIndexesRejectsOpenEndRangeStartingPastTheListing(t *testing.T) {
	_, err := parseIndexes(fakeResolver{count: 3}, "indexes", []string{"5.."})
	if err == nil {
		t.Fatal("parseIndexes accepted '5..' starting past a 3-item listing")
	}
	want := "Invalid value for 'indexes': '5..' starts past the current listing (3 items)."
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

// An open-end range propagates the resolver's error (e.g. no state saved
// yet) directly, rather than reporting it as a malformed range.
func TestParseIndexesPropagatesCountErrorForOpenEndRange(t *testing.T) {
	wantErr := errors.New("no state found")
	_, err := parseIndexes(fakeResolver{countErr: wantErr}, "indexes", []string{"5.."})
	if err != wantErr {
		t.Errorf("parseIndexes(%q) error = %v, want %v", "5..", err, wantErr)
	}
}

// An open-end range still respects maxRangeSpan once its end is resolved.
func TestParseIndexesRejectsOversizedOpenEndRange(t *testing.T) {
	_, err := parseIndexes(fakeResolver{count: 999999}, "indexes", []string{"1.."})
	if err == nil {
		t.Fatal("parseIndexes accepted an oversized open-end range")
	}
}

// A pathological range shouldn't build a giant slice before any index is even
// resolved against the current listing.
func TestParseIndexesRejectsOversizedRanges(t *testing.T) {
	_, err := parseIndexes(fakeResolver{}, "indexes", []string{"1..999999"})
	if err == nil {
		t.Fatal("parseIndexes accepted an oversized range")
	}
}

// A range whose ends sit near opposite bounds of int must still be rejected
// as oversized, not silently accepted via an overflowed span that bypasses
// maxRangeSpan and then loops from one end of int to the other. Run with a
// timeout rather than a bare call: on the overflow this regresses, the
// unbounded loop would otherwise hang the test (and eventually the CI
// runner) instead of failing it cleanly.
func TestParseIndexesRejectsOverflowingRanges(t *testing.T) {
	arg := fmt.Sprintf("%d..%d", math.MaxInt64, math.MinInt64)
	done := make(chan error, 1)
	go func() {
		_, err := parseIndexes(fakeResolver{}, "indexes", []string{arg})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("parseIndexes accepted a range whose span overflows int")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("parseIndexes did not return — span overflow likely bypassed the maxRangeSpan guard")
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
		if err := cmd.Args(cmd, []string{"1", "3..5"}); err != nil {
			t.Errorf("%s rejects a mixed index/range argument list: %v", name, err)
		}
		if !strings.Contains(cmd.Use, "...") {
			t.Errorf("%s: Use = %q, want it to show a repeatable index", name, cmd.Use)
		}
	}
}

// indexResolverFunc lets a test fail on a specific index rather than
// uniformly, so validateIndexes can be proven to check every index in the
// batch rather than stopping after the first one resolves.
type indexResolverFunc func(int) (string, string, kinds.Kind, error)

func (f indexResolverFunc) Fields(idx int) (string, string, kinds.Kind, error) {
	return f(idx)
}

func (f indexResolverFunc) Count() (int, error) {
	return 0, nil
}

func TestValidateIndexesCatchesABadIndexAnywhereInTheBatch(t *testing.T) {
	resolver := indexResolverFunc(func(idx int) (string, string, kinds.Kind, error) {
		if idx == 3 {
			return "", "", "", fmt.Errorf("index %d is out of range", idx)
		}
		return "pod", "prod", kinds.Pod, nil
	})
	if err := validateIndexes(resolver, []int{1, 2, 3, 4}); err == nil {
		t.Fatal("validateIndexes accepted a batch containing an out-of-range index")
	}
}

func TestValidateIndexesAcceptsAWhollyValidBatch(t *testing.T) {
	resolver := indexResolverFunc(func(idx int) (string, string, kinds.Kind, error) {
		return "pod", "prod", kinds.Pod, nil
	})
	if err := validateIndexes(resolver, []int{1, 2, 3}); err != nil {
		t.Fatalf("validateIndexes rejected a wholly valid batch: %v", err)
	}
}

// A range that runs past the current listing must not delete anything —
// not even the indexes that were in range — because there is no way to undo
// a delete once it has run. Validating every index before acting on any of
// them is the only way to make a bad range fail cleanly instead of partially.
func TestDeleteValidatesAllIndexesBeforeDeletingAny(t *testing.T) {
	kube := &recordingKubectl{}
	services := switchServices(t, kube)
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"nginx", "redis"}, kinds.Pod),
		Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save pods: %v", err)
	}

	cmd := newDeleteCommand(services)
	cmd.SetArgs([]string{"1", "2..5", "-y"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("delete succeeded despite an out-of-range index in the batch")
	}
	if len(kube.runs) != 0 {
		t.Errorf("kubectl was called %d times, want 0 — index 3 is out of range and "+
			"should be caught before index 1 or 2 is deleted", len(kube.runs))
	}
}

// switchServices wires the real state service against a temp file, so the
// listing path and the switch path meet the way they do in the tool.
//
// The package renderer is a global these tests have to write to — it is nil
// until configured, and they drive the listing path, which renders. Restoring
// it is registered here rather than left to each caller: a test that swaps in
// its own buffer and then fails before putting it back leaves every later test
// in the package writing somewhere it does not expect.
func switchServices(t *testing.T, kube kubectl.Service) Services {
	t.Helper()
	render.Configure("default", true)
	t.Cleanup(func() { render.Configure("default", true) })
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

// The caption rides back with the listing rather than coming from a second
// lookup: `kubectl config current-context` is a subprocess, and listing contexts
// should spawn it once.
func TestContextListingReadsTheCurrentContextOnce(t *testing.T) {
	kube := &recordingKubectl{
		output: "CURRENT   NAME             CLUSTER\n*         docker-desktop   docker-desktop\n",
	}
	services := switchServices(t, kube)

	if err := listSwitchTargets(services, true); err != nil {
		t.Fatalf("listSwitchTargets: %v", err)
	}
	if kube.contextReads != 1 {
		t.Errorf("read the current context %d times, want 1", kube.contextReads)
	}
}

// `kx state --targets` reads the slots, which are not in the history stack, so
// it must work when the stack is empty — that is the fresh-install shape.
func TestStateTargetsRendersSlotsWithoutHistory(t *testing.T) {
	kube := &recordingKubectl{output: namespaceTable}
	services := switchServices(t, kube)
	if err := listSwitchTargets(services, false); err != nil {
		t.Fatalf("listSwitchTargets: %v", err)
	}

	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")

	cmd := newStateCommand(services)
	cmd.SetArgs([]string{"--targets"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx state --targets: %v", err)
	}

	got := out.String()
	for _, want := range []string{"Namespaces", "default", "prod", "NAME"} {
		if !strings.Contains(got, want) {
			t.Errorf("output = %q, want it to contain %q", got, want)
		}
	}
}

// The flag is registered, so it shows up in --help rather than working silently.
func TestStateTargetsFlagIsRegistered(t *testing.T) {
	cmd := newStateCommand(Services{})
	if cmd.Flags().Lookup("targets") == nil {
		t.Error("kx state has no --targets flag")
	}
	if flag := cmd.Flags().ShorthandLookup("t"); flag == nil || flag.Name != "targets" {
		t.Error("kx state has no -t shorthand for --targets")
	}
}

// No state file at all is the first thing a new install has. `--targets` and
// `--all` each describe their own view rather than reporting the raw
// "No state found", which names a command neither of them is about.
func TestStateViewsOnAnAbsentStateFile(t *testing.T) {
	for _, tc := range []struct{ flag, want string }{
		{"--targets", "No switch targets yet"},
		{"--all", "No history yet"},
	} {
		services := switchServices(t, &recordingKubectl{})
		var out bytes.Buffer
		render.SetOutput(&out, &out, "github-dark")

		cmd := newStateCommand(services)
		cmd.SetArgs([]string{tc.flag})
		err := cmd.Execute()

		if err != nil {
			t.Errorf("kx state %s on an absent state file: %v", tc.flag, err)
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Errorf("kx state %s output = %q, want %q", tc.flag, out.String(), tc.want)
		}
	}
}

// kx state drop --all clears history and slots after confirming, since it's
// destructive in a way a single-position drop isn't (that only costs a
// re-list of one entry; --all also discards the namespace/context slots).
func TestDropAllConfirmsBeforeClearing(t *testing.T) {
	services := switchServices(t, &recordingKubectl{output: namespaceTable})
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"nginx"}, kinds.Pod),
		Namespace: "default",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var prompted string
	services.Confirm = func(m string) error { prompted = m; return nil }

	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")

	cmd := newDropCommand(services, "kx state drop")
	cmd.SetArgs([]string{"--all"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx state drop --all: %v", err)
	}
	if prompted == "" {
		t.Error("kx state drop --all did not prompt for confirmation")
	}

	history, err := services.State.LoadHistory()
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(history.States) != 0 {
		t.Errorf("len(States) = %d, want 0 after drop --all", len(history.States))
	}
}

// Declining the prompt must clear nothing.
func TestDropAllAbortsWithoutConfirmation(t *testing.T) {
	services := switchServices(t, &recordingKubectl{output: namespaceTable})
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"nginx"}, kinds.Pod),
		Namespace: "default",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	services.Confirm = func(string) error { return errors.New("aborted") }

	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")

	cmd := newDropCommand(services, "kx state drop")
	cmd.SetArgs([]string{"--all"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("kx state drop --all succeeded despite an aborted confirmation")
	}

	history, err := services.State.LoadHistory()
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(history.States) != 1 {
		t.Errorf("len(States) = %d, want 1 — an aborted drop --all must not clear anything", len(history.States))
	}
}

func TestCopyRegistersHelpFlags(t *testing.T) {
	cmd := newCopyCommand(Services{})
	for _, name := range []string{"container", "no-preserve", "retries"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("--%s is not registered, so it will not appear in --help", name)
		}
	}
}

func TestCopyRequiresTwoArguments(t *testing.T) {
	cmd := newCopyCommand(Services{})
	if err := cmd.RunE(cmd, []string{"1:/etc/foo"}); err == nil {
		t.Error("accepted a single argument, want src and dest required")
	}
}

// The real bug this guards: an Args validator requiring 2 arguments runs
// against the unstripped argv on the full cobra dispatch path (cmd.RunE
// alone never invokes Args, which is why that shortcut wouldn't have caught
// this) — `kx cp --help` is a single argument, so a MinimumNArgs(2) gate
// rejects it with an arity error before RunE ever sees it and gets a chance
// to recognize --help via passthrough.
func TestCopyHelpDoesNotTriggerAnArityError(t *testing.T) {
	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")
	cmd := newCopyCommand(Services{})
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("Execute with --help = %v, want nil (help handled, not an arity error)", err)
	}
}

// port-forward had the identical bug, caught and fixed alongside cp's own
// (both took a MinimumNArgs(2) validator from the same pattern).
func TestPortForwardHelpDoesNotTriggerAnArityError(t *testing.T) {
	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")
	cmd := newPortForwardCommand(Services{})
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("Execute with --help = %v, want nil (help handled, not an arity error)", err)
	}
}
