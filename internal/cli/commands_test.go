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

// The leading run of numbers, ranges and marks are references; everything
// after is kubectl's. A range token counts as a single leading argument
// here — it's expanded later, in parseRefs.
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
		// parseRefs for a proper "not a valid range" error, rather than
		// falling through to the generic "not a valid int" message that
		// applies when the leading run is empty.
		{[]string{"5..", "-o", "wide"}, 1, "-o wide"},
		// A mark belongs to the leading run unvalidated, the same as a
		// malformed range: "kx logs @nope" must reach parseRefs for the
		// unknown-mark error, not fall through to kubectl as a positional
		// and produce "@nope is not a valid int".
		{[]string{"@api", "-o", "wide"}, 1, "-o wide"},
		{[]string{"@api", "2", "--tail=5"}, 2, "--tail=5"},
		// A flag ends the run even when its value holds "..", which a range
		// check alone would have taken for a malformed range.
		{[]string{"1", "--since=.."}, 1, "--since=.."},
		{[]string{"--output=jsonpath={..metadata.name}"}, 0, "--output=jsonpath={..metadata.name}"},
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

// refIndexes extracts the Index of every ref, for tests migrated from
// parseIndexes ([]int) to parseRefs ([]state.Ref) that only ever exercise the
// plain-index path — a Mark in the result here would zero-value to 0 and hide
// a real bug, so callers that care about marks assert on the Refs directly
// instead of going through this.
func refIndexes(refs []state.Ref) []int {
	indexes := make([]int, len(refs))
	for i, ref := range refs {
		indexes[i] = ref.Index
	}
	return indexes
}

// A range token expands into every index it spans, inclusive of both ends,
// walking in whichever direction the two ends imply.
func TestParseRefsExpandsRanges(t *testing.T) {
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
			got, err := parseRefs(fakeResolver{}, "indexes", tc.args)
			if err != nil {
				t.Fatalf("parseRefs(%v) error = %v", tc.args, err)
			}
			if !reflect.DeepEqual(refIndexes(got), tc.want) {
				t.Errorf("parseRefs(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// A malformed range is reported the same way a bad single index is: named,
// quoted, and rejected before reaching the cluster.
func TestParseRefsRejectsMalformedRanges(t *testing.T) {
	cases := []struct {
		arg  string
		want string
	}{
		{"9..abc", "Invalid value for 'indexes': '9..abc' is not a valid range."},
		{"9...17", "Invalid value for 'indexes': '9...17' is not a valid range."},
	}
	for _, tc := range cases {
		t.Run(tc.arg, func(t *testing.T) {
			_, err := parseRefs(fakeResolver{}, "indexes", []string{tc.arg})
			if err == nil {
				t.Fatalf("parseRefs(%q) accepted a malformed range", tc.arg)
			}
			if err.Error() != tc.want {
				t.Errorf("error = %q, want %q", err, tc.want)
			}
		})
	}
}

// "..5" defaults the start to 1; "5.." defaults the end to the size of the
// current listing, reported by the resolver's Count().
func TestParseRefsExpandsOpenRanges(t *testing.T) {
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
			got, err := parseRefs(resolver, "indexes", tc.args)
			if err != nil {
				t.Fatalf("parseRefs(%v) error = %v", tc.args, err)
			}
			if !reflect.DeepEqual(refIndexes(got), tc.want) {
				t.Errorf("parseRefs(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// A bare ".." is not "everything" — it's rejected the same way a malformed
// range is, so a destructive command like delete never expands an
// unqualified ".." into the whole listing by accident.
func TestParseRefsRejectsBareDoubleDot(t *testing.T) {
	_, err := parseRefs(fakeResolver{}, "indexes", []string{".."})
	if err == nil {
		t.Fatal("parseRefs accepted a bare '..'")
	}
	want := "Invalid value for 'indexes': '..' is not a valid range."
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

// An open-end range whose start is already past the current listing is a
// hard error — not a silent reverse-walk (unlike an explicit "20..9") and
// not a silent no-op.
func TestParseRefsRejectsOpenEndRangeStartingPastTheListing(t *testing.T) {
	_, err := parseRefs(fakeResolver{count: 3}, "indexes", []string{"5.."})
	if err == nil {
		t.Fatal("parseRefs accepted '5..' starting past a 3-item listing")
	}
	want := "Invalid value for 'indexes': '5..' starts past the current listing (3 items)."
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

// An open-end range propagates the resolver's error (e.g. no state saved
// yet) directly, rather than reporting it as a malformed range.
func TestParseRefsPropagatesCountErrorForOpenEndRange(t *testing.T) {
	wantErr := errors.New("no state found")
	_, err := parseRefs(fakeResolver{countErr: wantErr}, "indexes", []string{"5.."})
	if err != wantErr {
		t.Errorf("parseRefs(%q) error = %v, want %v", "5..", err, wantErr)
	}
}

// An open-end range still respects maxRangeSpan once its end is resolved.
func TestParseRefsRejectsOversizedOpenEndRange(t *testing.T) {
	_, err := parseRefs(fakeResolver{count: 999999}, "indexes", []string{"1.."})
	if err == nil {
		t.Fatal("parseRefs accepted an oversized open-end range")
	}
	want := "Invalid value for 'indexes': '1..' spans more than 10000 indexes."
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

// A pathological range shouldn't build a giant slice before any index is even
// resolved against the current listing.
func TestParseRefsRejectsOversizedRanges(t *testing.T) {
	_, err := parseRefs(fakeResolver{}, "indexes", []string{"1..999999"})
	if err == nil {
		t.Fatal("parseRefs accepted an oversized range")
	}
}

// A range whose ends sit near opposite bounds of int must still be rejected
// as oversized, not silently accepted via an overflowed span that bypasses
// maxRangeSpan and then loops from one end of int to the other. Run with a
// timeout rather than a bare call: on the overflow this regresses, the
// unbounded loop would otherwise hang the test (and eventually the CI
// runner) instead of failing it cleanly.
func TestParseRefsRejectsOverflowingRanges(t *testing.T) {
	arg := fmt.Sprintf("%d..%d", math.MaxInt64, math.MinInt64)
	done := make(chan error, 1)
	go func() {
		_, err := parseRefs(fakeResolver{}, "indexes", []string{arg})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("parseRefs accepted a range whose span overflows int")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("parseRefs did not return — span overflow likely bypassed the maxRangeSpan guard")
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
		"labels":      newMetadataReadCommand(services, "labels", "", "", "labels", "LABEL", true),
		"annotations": newMetadataReadCommand(services, "annotations", "", "", "annotations", "ANNOTATION", false),
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
// uniformly, so resolveRefs can be proven to check every index in the
// batch rather than stopping after the first one resolves.
type indexResolverFunc func(int) (string, string, kinds.Kind, error)

func (f indexResolverFunc) Fields(idx int) (string, string, kinds.Kind, error) {
	return f(idx)
}

func (f indexResolverFunc) Resolve(ref state.Ref) (string, string, kinds.Kind, error) {
	return f(ref.Index)
}

func (f indexResolverFunc) ResolveExpecting(ref state.Ref, expected kinds.Kind) (string, string, error) {
	name, namespace, _, err := f(ref.Index)
	return name, namespace, err
}

func (f indexResolverFunc) Count() (int, error) {
	return 0, nil
}

func TestResolveRefsCatchesABadIndexAnywhereInTheBatch(t *testing.T) {
	resolver := indexResolverFunc(func(idx int) (string, string, kinds.Kind, error) {
		if idx == 3 {
			return "", "", "", fmt.Errorf("index %d is out of range", idx)
		}
		return "pod", "prod", kinds.Pod, nil
	})
	if _, err := resolveRefs(resolver, "indexes", []string{"1", "2", "3", "4"}); err == nil {
		t.Fatal("resolveRefs accepted a batch containing an out-of-range index")
	}
}

func TestResolveRefsAcceptsAWhollyValidBatch(t *testing.T) {
	resolver := indexResolverFunc(func(idx int) (string, string, kinds.Kind, error) {
		return "pod", "prod", kinds.Pod, nil
	})
	if _, err := resolveRefs(resolver, "indexes", []string{"1", "2", "3"}); err != nil {
		t.Fatalf("resolveRefs rejected a wholly valid batch: %v", err)
	}
}

// An out-of-range index in a batch must not delete anything — not even the
// indexes that were in range — because there is no way to undo a delete once
// it has run. Validating every index before acting on any of them is the only
// way to make a bad batch fail cleanly instead of partially.
//
// Spelled with an explicit index rather than a range: a range is now clamped
// to the listing (see TestDeleteClampsARangeRatherThanRefusingTheBatch), so
// "2..5" no longer carries an out-of-range index to be caught. An index the
// user typed out still does, and this is the guarantee that matters for it.
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
	cmd.SetArgs([]string{"1", "99", "-y"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("delete succeeded despite an out-of-range index in the batch")
	}
	if len(kube.runs) != 0 {
		t.Errorf("kubectl was called %d times, want 0 — index 3 is out of range and "+
			"should be caught before index 1 or 2 is deleted", len(kube.runs))
	}
}

// A literal 0 is an out-of-range index like any other, not an absent
// reference. state.Ref{} — the zero value — used to be treated specially by
// Resolve (an "empty ref" guard reported "No resource reference given."), but
// parseIndex has no zero guard of its own: a user typing `kx describe 0`
// produces exactly that same zero Ref, and telling that apart from a
// programmer-constructed empty one is not possible from the Ref alone. The
// guard's message was accurate for one and wrong for the other, and there was
// no way to know which had happened — so develop's actual behavior (an
// out-of-range message naming the listing) is the only one that is right for
// what a user who types "0" gets, and this pins it end to end.
func TestDescribeOnLiteralZeroReportsOutOfRange(t *testing.T) {
	kube := &recordingKubectl{}
	services := switchServices(t, kube)
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"nginx", "redis"}, kinds.Pod),
		Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save pods: %v", err)
	}

	cmd := newDescribeCommand(services)
	cmd.SetArgs([]string{"0"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("kx describe 0 succeeded, want an out-of-range error")
	}
	for _, want := range []string{"Index 0", "out of range", "2 Pods"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q\n  missing %q", err, want)
		}
	}
	if len(kube.interactive) != 0 {
		t.Errorf("kubectl was called %d times, want 0 for an unresolvable index", len(kube.interactive))
	}
}

// Open ranges resolve against the real listing end-to-end, the same way an
// explicit range does.
func TestDeleteAcceptsOpenRanges(t *testing.T) {
	cases := []struct {
		name      string
		arg       string
		want      int      // number of kubectl delete calls expected
		wantNames []string // resource names, in the order they should be deleted
	}{
		{"open start", "..2", 2, []string{"nginx", "redis"}},
		{"open end", "2..", 2, []string{"redis", "web"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kube := &recordingKubectl{}
			services := switchServices(t, kube)
			if err := services.State.Save(state.State{
				Resources: state.NewResources([]string{"nginx", "redis", "web"}, kinds.Pod),
				Namespace: "prod",
			}); err != nil {
				t.Fatalf("Save pods: %v", err)
			}

			cmd := newDeleteCommand(services)
			cmd.SetArgs([]string{tc.arg, "-y"})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("delete %q: %v", tc.arg, err)
			}
			if len(kube.runs) != tc.want {
				t.Errorf("delete %q ran kubectl %d times, want %d", tc.arg, len(kube.runs), tc.want)
			}
			// Each recorded run is "delete <kind> <name> -n <namespace>" — see
			// DeleteCommand.Execute in resource.go — so the name is args[2].
			for i, wantName := range tc.wantNames {
				if i >= len(kube.runs) {
					break
				}
				if got := kube.runs[i][2]; got != wantName {
					t.Errorf("delete %q call %d deleted %q, want %q", tc.arg, i, got, wantName)
				}
			}
		})
	}
}

// An open-end range whose start is already past the current listing must not
// delete anything — the #211 batch-validation guarantee extends to open
// ranges, since the "past the listing" check runs during parsing, before any
// index is acted on.
func TestDeleteRejectsOpenEndRangeStartingPastTheListing(t *testing.T) {
	kube := &recordingKubectl{}
	services := switchServices(t, kube)
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"nginx", "redis"}, kinds.Pod),
		Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save pods: %v", err)
	}

	cmd := newDeleteCommand(services)
	cmd.SetArgs([]string{"5..", "-y"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("delete succeeded despite '5..' starting past a 2-item listing")
	}
	if !strings.Contains(err.Error(), "starts past the current listing") {
		t.Errorf("error = %q, want it to mention starting past the current listing", err)
	}
	if len(kube.runs) != 0 {
		t.Errorf("kubectl was called %d times, want 0", len(kube.runs))
	}
}

func TestDescribeRejectsOpenEndRangeStartingPastTheListing(t *testing.T) {
	kube := &recordingKubectl{}
	services := switchServices(t, kube)
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"nginx", "redis"}, kinds.Pod),
		Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save pods: %v", err)
	}

	cmd := newDescribeCommand(services)
	cmd.SetArgs([]string{"5.."})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("describe succeeded despite '5..' starting past a 2-item listing")
	}
	if !strings.Contains(err.Error(), "starts past the current listing") {
		t.Errorf("error = %q, want it to mention starting past the current listing", err)
	}
	if len(kube.interactive) != 0 {
		t.Errorf("kubectl was called %d times, want 0", len(kube.interactive))
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

// kx ns and kx context read a slot (FieldsNamed), which the marks work
// deliberately left untouched — a slot is not a resource reference, and
// there is nothing for `kx mark` to have pinned there. newSwitchCommand used
// to extract ref.Index in isolation after parseRef, so a mark's zero-value
// Index silently went through as index 0 rather than being refused: `kx ns
// @foo` resolved index 0 against whatever namespace listing happened to be
// current and reported a confusing out-of-range error that named no mark at
// all. Both spellings are checked, and the assertion is on the message
// itself — naming the mark and the reason — not merely that an error came
// back, since a bad error would have passed a looser check too.
func TestSwitchRefusesAMark(t *testing.T) {
	cases := []struct {
		use       string
		isContext bool
		wantNoun  string
	}{
		{"namespace", false, "namespaces are switched by index"},
		{"context", true, "contexts are switched by index"},
	}
	for _, tc := range cases {
		t.Run(tc.use, func(t *testing.T) {
			services := switchServices(t, &recordingKubectl{})
			cmd := newSwitchCommand(services, tc.use, tc.use, "short", tc.isContext)
			cmd.SetArgs([]string{"@foo"})
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("kx %s @foo succeeded, want a refusal", tc.use)
			}
			if !strings.Contains(err.Error(), "@foo") {
				t.Errorf("err = %q, want it to name the mark", err)
			}
			if !strings.Contains(err.Error(), tc.wantNoun) {
				t.Errorf("err = %q, want %q", err, tc.wantNoun)
			}
		})
	}
}

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

// Switching does not rewrite the slot, so the namespace saved with it is where
// the caller was when they last listed. `kx state --targets` is read after a
// switch as often as before one, and it was answering "where am I" with the
// namespace already left behind.
func TestStateTargetsCaptionsTheLiveNamespace(t *testing.T) {
	kube := &recordingKubectl{output: namespaceTable}
	services := switchServices(t, kube)
	// List the slot standing in prod, which is what recordingKubectl reports
	// until it is moved.
	if err := listSwitchTargets(services, false); err != nil {
		t.Fatalf("listSwitchTargets: %v", err)
	}
	// Then switch. `kx ns <n>` edits kubeconfig and leaves the slot alone.
	kube.namespace = "diagnostics"

	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")
	cmd := newStateCommand(services)
	cmd.SetArgs([]string{"--targets"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx state --targets: %v", err)
	}

	caption := strings.SplitN(out.String(), "\n", 2)[0]
	if !strings.Contains(caption, "diagnostics") {
		t.Errorf("caption = %q, want the namespace the caller is in now", caption)
	}
	if strings.Contains(caption, "prod") {
		t.Errorf("caption = %q, want the namespace the slot was listed in out of it", caption)
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

	cmd := newDropCommand(services)
	cmd.SetArgs([]string{"--all"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx state drop --all: %v", err)
	}
	// The wording is asserted, not merely its presence. --all no longer means
	// "all" — marks survive it — so the prompt is the one place the user learns
	// that before deciding, and a silent reword would take it away.
	for _, want := range []string{
		"namespace and context slots", "Marks are untouched", "kx unmark --all",
	} {
		if !strings.Contains(prompted, want) {
			t.Errorf("prompt = %q\n  missing %q", prompted, want)
		}
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

	cmd := newDropCommand(services)
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

// `kx debug --` clears the arity check on the unstripped argv and leaves
// nothing behind once the command is split off, so the guard has to name what
// is missing rather than let an empty index reach the resolver.
func TestDebugWithoutAnIndexSaysSo(t *testing.T) {
	cmd := newDebugCommand(Services{})

	err := executeArgv(cmd, []string{"--"})

	if err == nil {
		t.Fatal("debug with no index succeeded")
	}
	// requiredArgsError's wording, from debug's own Use string. The "kx "
	// prefix is missing because CommandPath climbs the parent chain and this
	// command has no root — the full sentence is pinned in help_test.go.
	if !strings.Contains(err.Error(), "requires <index>") {
		t.Errorf("err = %q, want it to name the missing index", err)
	}
}

// `kx ns` is a switch screen, and its caption answers "where am I now" —
// the only place on that screen the answer appears. #271 made Namespace
// cluster-scoped, which is right for `kx get namespaces` and silently emptied
// this caption on the way past: `Namespaces · prod · 2 items` became
// `Namespaces · 2 items`.
//
// Read live, per #240: a slot freezes when the listing is taken, and this has
// to be right after a switch, not after the last `kx ns`.
func TestNamespaceListingCaptionsTheCurrentNamespace(t *testing.T) {
	kube := &recordingKubectl{output: namespaceTable, namespace: "prod"}
	services := switchServices(t, kube)
	// After switchServices, which calls render.Configure and would discard it.
	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")

	if err := listSwitchTargets(services, false); err != nil {
		t.Fatalf("listSwitchTargets: %v", err)
	}
	// The caption line only — "prod" is also a row in the table below it.
	caption := strings.SplitN(out.String(), "\n", 2)[0]
	if !strings.Contains(caption, "prod") {
		t.Errorf("caption = %q, want it to name the current namespace", caption)
	}
}

// The error used to say only "'notanumber' is not a replica count" — no
// remedy, no closing period, and a different vocabulary than every other bad
// positional argument kx parses, which all say "Invalid value for 'name':
// 'value' is not a valid int."
func TestScaleRejectsANonNumericReplicaCount(t *testing.T) {
	cmd := newScaleCommand(Services{})
	err := cmd.RunE(cmd, []string{"1", "notanumber"})
	if err == nil {
		t.Fatal("kx scale 1 notanumber was accepted")
	}
	want := "Invalid value for 'replicas': 'notanumber' is not a valid int."
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

// The caption tracks the kubeconfig rather than the listing, so switching and
// listing again says where the caller is now.
func TestNamespaceListingCaptionFollowsASwitch(t *testing.T) {
	kube := &recordingKubectl{output: namespaceTable, namespace: "staging"}
	services := switchServices(t, kube)
	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")

	if err := listSwitchTargets(services, false); err != nil {
		t.Fatalf("listSwitchTargets: %v", err)
	}
	// staging is not one of the listed namespaces, so only the caption can
	// be the source of it — the listing itself cannot satisfy this.
	if !strings.Contains(out.String(), "staging") {
		t.Errorf("output = %q, want the caption to name the namespace the caller moved to", out.String())
	}
}

// runLogs drives `kx logs` through real argv and hands back the kubectl it was
// wired to. The --since rewrite happens in the argv split, which only runs
// inside RunE — a test that called the helper directly would agree with itself.
func runLogs(t *testing.T, argv ...string) (*recordingKubectl, error) {
	t.Helper()
	kube := &recordingKubectl{}
	services := switchServices(t, kube)
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"nginx"}, kinds.Pod),
		Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return kube, Execute(NewRoot(services, "test"), append([]string{"logs"}, argv...))
}

// sinceValue is the window kubectl was handed, read back with kubectl's own
// parser.
//
// Parsed rather than compared as text, because the requirement is that kubectl
// accepts what kx emits — pinning the exact spelling would test a cosmetic
// choice, and pass just as well on a spelling kubectl refuses.
func sinceValue(t *testing.T, args []string) time.Duration {
	t.Helper()
	for _, arg := range args {
		value, ok := strings.CutPrefix(arg, "--since=")
		if !ok {
			continue
		}
		window, err := time.ParseDuration(value)
		if err != nil {
			t.Fatalf("kubectl parses --since with time.ParseDuration and would "+
				"reject %q: %v", arg, err)
		}
		return window
	}
	t.Fatalf("no --since among the args kubectl was given: %v", args)
	return 0
}

// kubectl parses --since with time.ParseDuration, which stops at hours, so
// `kx logs 1 --since 7d` failed with `unknown unit "d"` while `kx diag --since
// 7d` worked. kx reads the day spelling itself and hands kubectl one it takes.
func TestLogsAcceptsTheDaySpellingKubectlRejects(t *testing.T) {
	for _, argv := range [][]string{
		{"1", "--since", "7d"},
		{"1", "--since=7d"},
	} {
		kube, err := runLogs(t, argv...)
		if err != nil {
			t.Fatalf("kx %v: %v", argv, err)
		}
		if len(kube.interactive) != 1 {
			t.Fatalf("kx %v ran kubectl %d times, want 1", argv, len(kube.interactive))
		}
		if got := sinceValue(t, kube.interactive[0]); got != 7*24*time.Hour {
			t.Errorf("kx %v gave kubectl %v, want a week", argv, got)
		}
	}
}

// A duration kubectl already accepts has to survive the round trip unchanged
// in meaning — the rewrite is a translation, not a reinterpretation.
func TestLogsKeepsADurationKubectlAlreadyUnderstands(t *testing.T) {
	kube, err := runLogs(t, "1", "--since=90m")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := sinceValue(t, kube.interactive[0]); got != 90*time.Minute {
		t.Errorf("kubectl got %v, want the 90m it was given", got)
	}
}

// --since-time is a different kubectl flag that takes an RFC3339 instant, and
// the two are spelled alike enough to be caught by a careless match.
func TestLogsLeavesSinceTimeAlone(t *testing.T) {
	const stamp = "--since-time=2026-09-09T06:00:00Z"
	kube, err := runLogs(t, "1", stamp)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	args := strings.Join(kube.interactive[0], " ")
	if !strings.Contains(args, stamp) {
		t.Errorf("kubectl args = %q, want %s passed through untouched", args, stamp)
	}
	if strings.Contains(args, "--since=") {
		t.Errorf("kubectl args = %q, want no --since invented from --since-time", args)
	}
}

// A malformed window is kx's error to report, in kx's voice, before a
// subprocess is spawned — not kubectl's after one is.
func TestLogsRejectsAMalformedSinceBeforeSpawningKubectl(t *testing.T) {
	kube, err := runLogs(t, "1", "--since", "7 weeks")
	if err == nil {
		t.Fatal("kx logs accepted '7 weeks'")
	}
	if !strings.Contains(err.Error(), "--since") {
		t.Errorf("err = %v, want it to name --since", err)
	}
	if len(kube.interactive) != 0 {
		t.Errorf("kubectl was called %d times, want 0", len(kube.interactive))
	}
}

// Parsed by hand, so registration is the only thing putting it on the help
// screen — the same trap passthrough.go documents for every other kx flag on a
// DisableFlagParsing command.
func TestLogsRegistersSinceFlag(t *testing.T) {
	if newLogsCommand(Services{}).Flags().Lookup("since") == nil {
		t.Error("--since is not registered, so it will not appear in --help")
	}
}

// Saving a listing that found nothing is what stops an index resolving against
// the listing before it, and the entry it saves costs a history slot. --empty
// is how those are swept back up, without the confirmation --all needs: an
// entry holding nothing is not work anyone can lose.
func TestDropEmptySweepsTheEmptyEntries(t *testing.T) {
	services := switchServices(t, &recordingKubectl{output: namespaceTable})
	for _, entry := range []state.State{
		{Resources: state.NewResources([]string{"nginx"}, kinds.Pod), Namespace: "default"},
		{Namespace: "empty", Query: &state.Query{Resource: "pods"}},
	} {
		if err := services.State.Save(entry); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	var prompted string
	services.Confirm = func(m string) error { prompted = m; return nil }

	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")

	cmd := newDropCommand(services)
	cmd.SetArgs([]string{"--empty"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx state drop --empty: %v", err)
	}
	if prompted != "" {
		t.Errorf("kx state drop --empty prompted %q, want no confirmation", prompted)
	}

	history, err := services.State.LoadHistory()
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(history.States) != 1 {
		t.Fatalf("len(States) = %d, want 1 — only the empty entry goes", len(history.States))
	}
	if names := history.States[0].Resources.Names(); len(names) != 1 || names[0] != "nginx" {
		t.Errorf("kept %v, want the entry that holds something", names)
	}
}

// Nothing to sweep is not an error, and not a silent reprint of an unchanged
// stack either — reprinting reads as though something happened.
func TestDropEmptyWithNothingToSweepSaysSo(t *testing.T) {
	services := switchServices(t, &recordingKubectl{output: namespaceTable})
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"nginx"}, kinds.Pod), Namespace: "default",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")

	cmd := newDropCommand(services)
	cmd.SetArgs([]string{"--empty"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx state drop --empty: %v", err)
	}
	if !strings.Contains(out.String(), "No empty") {
		t.Errorf("output = %q, want it to say there was nothing to drop", out.String())
	}
}

// --empty and a position are two different requests, and --all is a third.
func TestDropEmptyRefusesToBeCombined(t *testing.T) {
	services := switchServices(t, &recordingKubectl{output: namespaceTable})
	services.Confirm = func(string) error { return nil }

	for _, args := range [][]string{{"--empty", "2"}, {"--empty", "--all"}} {
		var out bytes.Buffer
		render.SetOutput(&out, &out, "github-dark")
		cmd := newDropCommand(services)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Errorf("kx state drop %v succeeded, want a refusal", args)
		}
	}
}

// The deliberate consequence of clamping, on the command where it matters
// most. `kx delete 1 2..5` on a two-row listing used to refuse the whole
// batch; it now deletes the two rows that exist, because that is what the
// range names once intersected with the listing.
//
// The all-or-nothing guarantee is unchanged for an index the user typed out —
// see TestDeleteValidatesAllIndexesBeforeDeletingAny — and a range entirely
// past the end is still refused rather than clamped to nothing.
func TestDeleteClampsARangeRatherThanRefusingTheBatch(t *testing.T) {
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
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx delete 1 2..5: %v", err)
	}
	if len(kube.runs) != 2 {
		t.Fatalf("kubectl calls = %d, want 2 — the rows that exist, each once", len(kube.runs))
	}

	cmd = newDeleteCommand(services)
	cmd.SetArgs([]string{"5..9", "-y"})
	if err := cmd.Execute(); err == nil {
		t.Error("kx delete 5..9 succeeded on a two-row listing, want the past-the-listing refusal")
	}
}

// kx ns is the switch screen, so it takes the marked listing. Asserted
// through the real call path, because the marker is only useful if the
// command that shows the screen is the one that asks for it.
func TestNamespaceListingMarksTheCurrentNamespace(t *testing.T) {
	kube := &recordingKubectl{output: namespaceTable, namespace: "prod"}
	services := switchServices(t, kube)

	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")
	if err := listSwitchTargets(services, false); err != nil {
		t.Fatalf("listSwitchTargets: %v", err)
	}

	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "prod") && !strings.HasPrefix(line, "Namespaces") {
			if !strings.Contains(line, "→") {
				t.Errorf("row = %q, want the current namespace marked", line)
			}
			return
		}
	}
	t.Errorf("output = %q, want a row for the current namespace", out.String())
}

// The real kubectl service hands kubectl's warnings to the renderer. Every
// command test builds Services with a fake, so without this the wiring could
// be dropped and nothing else would notice.
func TestNewServicesForwardsKubectlWarnings(t *testing.T) {
	t.Setenv("KX_STATE", filepath.Join(t.TempDir(), "state.json"))
	client, ok := NewServices(config.Default()).Kubectl.(*kubectl.Exec)
	if !ok {
		t.Fatalf("Kubectl is %T, want *kubectl.Exec", NewServices(config.Default()).Kubectl)
	}
	if client.Warn == nil {
		t.Error("kubectl.Exec.Warn is nil; kubectl's warnings are dropped")
	}
}

// kx describe parses kubectl describe's flags, so an index may follow them,
// a scope flag beside it is refused wherever it sits, and a flag kubectl
// describe lacks is refused before kubectl runs.
func TestDescribeParsesKubectlFlags(t *testing.T) {
	describe := func(args ...string) (*recordingKubectl, error) {
		kube := &recordingKubectl{output: "ok"}
		services := switchServices(t, kube)
		if err := services.State.Save(state.State{
			Resources: state.NewResources([]string{"nginx"}, kinds.Pod),
			Namespace: "prod",
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		quietRender(t)
		return kube, executeArgv(newDescribeCommand(services), args)
	}

	kube, err := describe("--show-events=false", "1")
	if err != nil {
		t.Fatalf("kx describe --show-events=false 1: %v", err)
	}
	if len(kube.interactive) != 1 || joinArgs(kube.interactive[0]) != "describe Pod nginx -n prod --show-events=false" {
		t.Errorf("kubectl = %q, want the index after the flag resolved", kube.interactive)
	}

	// An argument after the indexes is kubectl's, ahead of its flags, as it
	// was when kx split argv by hand.
	kube, err = describe("1", "redis", "--show-events=false")
	if err != nil {
		t.Fatalf("kx describe 1 redis: %v", err)
	}
	if len(kube.interactive) != 1 || joinArgs(kube.interactive[0]) != "describe Pod nginx -n prod redis --show-events=false" {
		t.Errorf("kubectl = %q, want redis forwarded before the flags", kube.interactive)
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-n", "other", "1"}, "'--namespace' cannot be combined with an index"},
		{[]string{"--chunk-size", "500"}, "requires <index>"},
		{[]string{"1", "-o", "wide"}, "'-o' isn't a flag of kubectl describe"},
	} {
		kube, err := describe(tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("kx describe %s: err = %v, want %q", joinArgs(tc.args), err, tc.want)
		}
		if len(kube.interactive) > 0 {
			t.Errorf("kx describe %s: kubectl was called: %q", joinArgs(tc.args), kube.interactive)
		}
	}
}

// indexCommand runs a converted index command on argv against a listing of two
// prod pods, nginx and redis, and returns what kubectl was asked to run.
func indexCommand(t *testing.T, build func(Services) *cobra.Command, args ...string) (*recordingKubectl, error) {
	t.Helper()
	return indexCommandOn(t, kinds.Pod, build, args...)
}

// indexCommandOn is indexCommand with the two rows listed as kind.
func indexCommandOn(t *testing.T, kind kinds.Kind, build func(Services) *cobra.Command, args ...string) (*recordingKubectl, error) {
	t.Helper()
	kube := &recordingKubectl{output: "ok"}
	services := switchServices(t, kube)
	services.Confirm = func(string) error { return nil }
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"nginx", "redis"}, kind),
		Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	quietRender(t)
	return kube, executeArgv(build(services), args)
}

// kx logs parses kubectl logs' flags: an index may follow them, kx's own
// reading of --since and --follow sees them in any spelling, and an argument
// after the index is still kubectl's container.
func TestLogsParsesKubectlFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--tail", "5", "1"}, "logs nginx -n prod --tail=5"},
		{[]string{"1", "--since", "7d"}, "logs nginx -n prod --since=168h0m0s"},
		{[]string{"1", "web", "-f"}, "logs nginx -n prod web --follow"},
	} {
		kube, err := indexCommand(t, newLogsCommand, tc.args...)
		if err != nil {
			t.Errorf("kx logs %s: %v", joinArgs(tc.args), err)
			continue
		}
		if len(kube.interactive) != 1 || joinArgs(kube.interactive[0]) != tc.want {
			t.Errorf("kx logs %s: kubectl = %q, want %q", joinArgs(tc.args), kube.interactive, tc.want)
		}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-f", "1", "2"}, "--follow streams a single pod"},
		{[]string{"1", "--since", "soon"}, "'--since'"},
		{[]string{"1", "-o", "json"}, "'-o' isn't a flag of kubectl logs"},
		{[]string{"-n", "other", "1"}, "'--namespace' cannot be combined with an index"},
	} {
		kube, err := indexCommand(t, newLogsCommand, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("kx logs %s: err = %v, want %q", joinArgs(tc.args), err, tc.want)
		}
		if len(kube.interactive) > 0 {
			t.Errorf("kx logs %s: kubectl was called: %q", joinArgs(tc.args), kube.interactive)
		}
	}
}

// kx edit parses kubectl edit's flags: the index may follow them, and a flag
// kubectl edit lacks is refused before an editor opens.
func TestEditParsesKubectlFlags(t *testing.T) {
	kube, err := indexCommand(t, newEditCommand, "-o", "json", "2")
	if err != nil {
		t.Fatalf("kx edit -o json 2: %v", err)
	}
	if len(kube.interactive) != 1 || joinArgs(kube.interactive[0]) != "edit Pod redis -n prod --output=json" {
		t.Errorf("kubectl = %q, want row 2 edited with the flag", kube.interactive)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-n", "other", "1"}, "'--namespace' cannot be combined with an index"},
		{[]string{"1", "--tail", "5"}, "'--tail' isn't a flag of kubectl edit"},
		{[]string{"--windows-line-endings"}, "requires <index>"},
	} {
		kube, err := indexCommand(t, newEditCommand, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("kx edit %s: err = %v, want %q", joinArgs(tc.args), err, tc.want)
		}
		if len(kube.interactive) > 0 {
			t.Errorf("kx edit %s: kubectl was called: %q", joinArgs(tc.args), kube.interactive)
		}
	}
}

// kx exec parses kubectl exec's flags up to --, and everything after -- is the
// command to run: a flag-shaped argument there is the command's own, never
// kubectl's or kx's. That is also why a flag kx doesn't know cannot be passed
// after -- on kx exec, and the refusal doesn't offer it.
func TestExecParsesKubectlFlagsUpToTheCommand(t *testing.T) {
	kube, err := indexCommand(t, newExecCommand, "-c", "app", "1", "--", "sh", "-c", "-o json")
	if err != nil {
		t.Fatalf("kx exec -c app 1 -- sh -c '-o json': %v", err)
	}
	if len(kube.interactive) != 1 {
		t.Fatalf("kubectl = %q, want one exec", kube.interactive)
	}
	// Exactly: the command once, after --, and its -o never read as a flag.
	if got, want := joinArgs(kube.interactive[0]), "exec -it nginx -n prod --container=app -- sh -c -o json"; got != want {
		t.Errorf("kubectl = %q, want %q", got, want)
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-n", "other", "1"}, "'--namespace' cannot be combined with an index"},
		{[]string{"1", "--bogus"}, "Everything after -- is the command to run"},
	} {
		kube, err := indexCommand(t, newExecCommand, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("kx exec %s: err = %v, want %q", joinArgs(tc.args), err, tc.want)
		}
		if len(kube.interactive)+len(kube.probes) > 0 {
			t.Errorf("kx exec %s: kubectl was called: %q %q", joinArgs(tc.args), kube.interactive, kube.probes)
		}
	}
	if doc := commandHelp(newExecCommand(Services{})).Doc; strings.Contains(doc, "put it after `--`") {
		t.Errorf("kx exec --help offers -- for kubectl's flags, but -- starts the command:\n%s", doc)
	}
}

// kx debug parses kubectl debug's flags up to --. Its --image is kx's to read
// as well as kubectl's, in any spelling and any position; its --profile is
// kubectl debug's own, which shadows kubectl's global --profile.
func TestDebugParsesKubectlFlagsUpToTheCommand(t *testing.T) {
	kube, err := indexCommand(t, newDebugCommand, "--image", "alpine", "1", "--profile", "general", "--", "ls", "-o")
	if err != nil {
		t.Fatalf("kx debug --image alpine 1 --profile general -- ls -o: %v", err)
	}
	if len(kube.interactive) != 1 {
		t.Fatalf("kubectl = %q, want one debug", kube.interactive)
	}
	got := joinArgs(kube.interactive[0])
	for _, want := range []string{"alpine", "--profile=general"} {
		if !strings.Contains(got, want) {
			t.Errorf("kubectl = %q, want it to carry %q", got, want)
		}
	}
	if !strings.HasSuffix(got, " -- ls -o") || strings.Count(got, "ls -o") != 1 {
		t.Errorf("kubectl = %q, want the command once, after --", got)
	}
	if profile := newDebugCommand(Services{}).Flags().Lookup("profile"); profile == nil ||
		!strings.Contains(profile.Usage, "general") {
		t.Errorf("--profile = %+v, want kubectl debug's own, not the global pprof flag", profile)
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--image=x"}, "requires <index>"},
		{[]string{"-n", "other", "1"}, "'--namespace' cannot be combined with an index"},
		{[]string{"1", "--bogus"}, "Everything after -- is the command to run"},
	} {
		kube, err := indexCommand(t, newDebugCommand, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("kx debug %s: err = %v, want %q", joinArgs(tc.args), err, tc.want)
		}
		if len(kube.interactive) > 0 {
			t.Errorf("kx debug %s: kubectl was called: %q", joinArgs(tc.args), kube.interactive)
		}
	}
}

// An explicitly empty --image, which `--image "$IMG"` with IMG unset produces,
// was forwarded as --image= beside the configured --image=busybox, leaving
// kubectl two. Whichever won, an ephemeral container can't be removed once
// attached, so it is refused before kubectl runs.
func TestDebugRefusesAnEmptyImage(t *testing.T) {
	for _, args := range [][]string{
		{"1", "--image", ""},
		{"1", "--image="},
	} {
		kube, err := indexCommand(t, newDebugCommand, args...)
		if err == nil || !strings.Contains(err.Error(), "'--image' was given an empty value") {
			t.Errorf("kx debug %q: err = %v, want the empty --image refused", args, err)
		}
		if len(kube.interactive) > 0 {
			t.Errorf("kx debug %q: kubectl was called: %q", args, kube.interactive)
		}
	}
}

// kx delete parses kubectl delete's flags. --dry-run is kx's to read as well
// as kubectl's, and takes kubectl's shape: bare, it is a dry run on its own
// and takes nothing after it — kx registers it as a string, which alone would
// have swallowed the -y that follows as its value.
func TestDeleteParsesKubectlFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"1", "--dry-run", "-y"}, "delete Pod nginx -n prod --dry-run"},
		{[]string{"-y", "--dry-run=client", "2"}, "delete Pod redis -n prod --dry-run=client"},
		{[]string{"--force", "--grace-period", "0", "1", "-y"}, "delete Pod nginx -n prod --force --grace-period=0"},
	} {
		kube, err := indexCommand(t, newDeleteCommand, tc.args...)
		if err != nil {
			t.Errorf("kx delete %s: %v", joinArgs(tc.args), err)
			continue
		}
		calls := append(kube.runs, kube.interactive...)
		if len(calls) != 1 || joinArgs(calls[0]) != tc.want {
			t.Errorf("kx delete %s: kubectl = %q, want %q", joinArgs(tc.args), calls, tc.want)
		}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-n", "other", "1", "-y"}, "'--namespace' cannot be combined with an index"},
		{[]string{"1", "--bogus", "-y"}, "'--bogus' isn't a flag of kubectl delete"},
		{[]string{"-y"}, "requires <index>"},
	} {
		kube, err := indexCommand(t, newDeleteCommand, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("kx delete %s: err = %v, want %q", joinArgs(tc.args), err, tc.want)
		}
		if len(kube.runs)+len(kube.interactive) > 0 {
			t.Errorf("kx delete %s: kubectl was called: %q %q", joinArgs(tc.args), kube.runs, kube.interactive)
		}
	}
}

// A positional left after a mutating command's arguments is refused, not
// forwarded: kubectl read it as one more resource, so kx delete 1 web -y
// deleted web as well as the nginx it reported (#507). --cascade orphan, bare,
// left orphan behind that way, and the refusal says how to attach it.
func TestMutatingCommandsRefuseAStrayPositional(t *testing.T) {
	for _, tc := range []struct {
		name  string
		kind  kinds.Kind
		build func(Services) *cobra.Command
		args  []string
		want  string
	}{
		{"delete", kinds.Pod, newDeleteCommand, []string{"1", "web", "-y"}, "'web' isn't an argument of delete"},
		{"delete", kinds.Pod, newDeleteCommand, []string{"1", "2", "web", "-y"}, "'web' isn't an argument of delete"},
		{"delete", kinds.Pod, newDeleteCommand, []string{"1", "--cascade", "orphan", "-y"},
			"attach it: '--cascade=orphan'"},
		{"scale", kinds.Deployment, newScaleCommand, []string{"1", "3", "web"}, "'web' isn't an argument of scale"},
		{"drain", kinds.Node, newDrainCommand, []string{"1", "node-b", "-y"}, "'node-b' isn't an argument of drain"},
		{"rollout", kinds.Deployment, newRolloutCommand, []string{"restart", "1", "web"},
			"'web' isn't an argument of rollout"},
	} {
		kube, err := indexCommandOn(t, tc.kind, tc.build, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("kx %s %s: err = %v, want %q", tc.name, joinArgs(tc.args), err, tc.want)
		}
		if len(kube.runs)+len(kube.interactive) > 0 {
			t.Errorf("kx %s %s: kubectl was called: %q %q", tc.name, joinArgs(tc.args), kube.runs, kube.interactive)
		}
	}
}

// After -- a word is still kubectl's, forwarded unread: the refusal covers the
// positionals kx parsed, not the escape that exists to get past kx's parse.
func TestDeleteForwardsAfterDashUnread(t *testing.T) {
	kube, err := indexCommand(t, newDeleteCommand, "1", "-y", "--", "--raw-flag")
	if err != nil {
		t.Fatalf("kx delete 1 -y -- --raw-flag: %v", err)
	}
	if len(kube.runs) != 1 || joinArgs(kube.runs[0]) != "delete Pod nginx -n prod --raw-flag" {
		t.Errorf("kubectl = %q, want delete Pod nginx -n prod --raw-flag", kube.runs)
	}
}

// --yes is kx's, parsed by cobra wherever it sits: with it the prompt is never
// asked; without it a declined prompt deletes nothing.
func TestDeleteYesSkipsThePromptWhereverItSits(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		deletes bool
	}{
		{[]string{"-y", "1"}, true},
		{[]string{"1", "--yes"}, true},
		{[]string{"1"}, false},
	} {
		kube := &recordingKubectl{output: "ok"}
		services := switchServices(t, kube)
		asked := false
		services.Confirm = func(string) error { asked = true; return errors.New("declined") }
		if err := services.State.Save(state.State{
			Resources: state.NewResources([]string{"nginx"}, kinds.Pod), Namespace: "prod",
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		quietRender(t)
		_ = executeArgv(newDeleteCommand(services), tc.args)
		if deleted := len(kube.runs) > 0; deleted != tc.deletes || asked == tc.deletes {
			t.Errorf("kx delete %s: deleted=%v asked=%v, want deleted=%v", joinArgs(tc.args), deleted, asked, tc.deletes)
		}
	}
}

// kx scale parses kubectl scale's flags: the index and the count may sit
// among them, and --replicas is still refused beside a count in any spelling.
func TestScaleParsesKubectlFlags(t *testing.T) {
	kube, err := indexCommandOn(t, kinds.Deployment, newScaleCommand, "--current-replicas", "2", "1", "3")
	if err != nil {
		t.Fatalf("kx scale --current-replicas 2 1 3: %v", err)
	}
	if len(kube.runs) != 1 || !strings.Contains(joinArgs(kube.runs[0]), "--replicas=3") ||
		!strings.Contains(joinArgs(kube.runs[0]), "--current-replicas=2") {
		t.Errorf("kubectl = %q, want --replicas=3 and --current-replicas=2", kube.runs)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"1", "3", "--replicas", "4"}, "'--replicas' cannot be combined with a replica count"},
		{[]string{"-n", "other", "1", "3"}, "'--namespace' cannot be combined with an index"},
		{[]string{"1", "3", "--bogus"}, "'--bogus' isn't a flag of kubectl scale"},
		{[]string{"1", "--timeout=1m"}, "requires <index> <replicas>"},
		{[]string{"1", "x"}, "Invalid value for 'replicas': 'x' is not a valid int."},
	} {
		kube, err := indexCommandOn(t, kinds.Deployment, newScaleCommand, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("kx scale %s: err = %v, want %q", joinArgs(tc.args), err, tc.want)
		}
		if len(kube.runs) > 0 {
			t.Errorf("kx scale %s: kubectl was called: %q", joinArgs(tc.args), kube.runs)
		}
	}
}

// kx rollout registers every action's kubectl flags, and once the action is
// read, refuses a flag only another action defines, naming that action.
func TestRolloutParsesEachActionsFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"undo", "--to-revision", "2", "1"}, "--to-revision=2"},
		{[]string{"history", "1", "--revision=3"}, "--revision=3"},
		{[]string{"status", "1", "--timeout", "2m"}, "--timeout=2m"},
	} {
		kube, err := indexCommandOn(t, kinds.Deployment, newRolloutCommand, tc.args...)
		if err != nil {
			t.Errorf("kx rollout %s: %v", joinArgs(tc.args), err)
			continue
		}
		calls := append(kube.runs, kube.interactive...)
		if len(calls) != 1 || !strings.Contains(joinArgs(calls[0]), "rollout "+tc.args[0]) ||
			!strings.Contains(joinArgs(calls[0]), tc.want) {
			t.Errorf("kx rollout %s: kubectl = %q, want %s with %s", joinArgs(tc.args), calls, tc.args[0], tc.want)
		}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"status", "1", "--to-revision=2"}, "'--to-revision' cannot be combined with 'status' — it is a flag of kubectl rollout undo."},
		{[]string{"restart", "1", "--revision", "3"}, "it is a flag of kubectl rollout status and kubectl rollout history."},
		{[]string{"-n", "other", "undo", "1"}, "'--namespace' cannot be combined with an index"},
		{[]string{"undo", "1", "--bogus"}, "'--bogus' isn't a flag of kubectl rollout status, restart, pause, resume, history or undo as of"},
		{[]string{"undo", "--timeout=1m"}, "requires <action> <index>"},
	} {
		kube, err := indexCommandOn(t, kinds.Deployment, newRolloutCommand, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("kx rollout %s: err = %v, want %q", joinArgs(tc.args), err, tc.want)
		}
		if len(kube.runs)+len(kube.interactive) > 0 {
			t.Errorf("kx rollout %s: kubectl was called: %q %q", joinArgs(tc.args), kube.runs, kube.interactive)
		}
	}
}

// kx port-forward parses kubectl port-forward's flags: the index and port may
// sit among them, and further ports are still kubectl's.
func TestPortForwardParsesKubectlFlags(t *testing.T) {
	kube, err := indexCommand(t, newPortForwardCommand, "--address", "0.0.0.0", "1", "8080:80", "9090:90")
	if err != nil {
		t.Fatalf("kx port-forward --address 0.0.0.0 1 8080:80 9090:90: %v", err)
	}
	if len(kube.interactive) != 1 || joinArgs(kube.interactive[0]) != "port-forward Pod/nginx 8080:80 -n prod 9090:90 --address=0.0.0.0" {
		t.Errorf("kubectl = %q, want both ports and the address", kube.interactive)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-n", "other", "1", "8080"}, "'--namespace' cannot be combined with an index"},
		{[]string{"1", "8080", "--bogus"}, "'--bogus' isn't a flag of kubectl port-forward"},
		{[]string{"1", "--address=0.0.0.0"}, "requires <index> <port>"},
	} {
		kube, err := indexCommand(t, newPortForwardCommand, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("kx port-forward %s: err = %v, want %q", joinArgs(tc.args), err, tc.want)
		}
		if len(kube.interactive) > 0 {
			t.Errorf("kx port-forward %s: kubectl was called: %q", joinArgs(tc.args), kube.interactive)
		}
	}
}

// kx cp parses kubectl cp's flags: source and destination may sit among them,
// and -c, --retries and --no-preserve reach kubectl in any spelling.
func TestCopyParsesKubectlFlags(t *testing.T) {
	kube, err := indexCommand(t, newCopyCommand, "-c", "app", "1:/etc/hosts", "./hosts", "--retries", "3")
	if err != nil {
		t.Fatalf("kx cp -c app 1:/etc/hosts ./hosts --retries 3: %v", err)
	}
	calls := append(kube.runs, kube.interactive...)
	if len(calls) != 1 || !strings.HasPrefix(joinArgs(calls[0]), "cp prod/nginx:/etc/hosts ./hosts") ||
		!strings.Contains(joinArgs(calls[0]), "--container=app") || !strings.Contains(joinArgs(calls[0]), "--retries=3") {
		t.Errorf("kubectl = %q, want the copy with --container=app and --retries=3", calls)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"1:/etc/hosts", "./hosts", "--bogus"}, "'--bogus' isn't a flag of kubectl cp"},
		{[]string{"1:/etc/hosts", "--retries=3"}, "requires <src> <dest>"},
	} {
		kube, err := indexCommand(t, newCopyCommand, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("kx cp %s: err = %v, want %q", joinArgs(tc.args), err, tc.want)
		}
		if len(kube.runs)+len(kube.interactive) > 0 {
			t.Errorf("kx cp %s: kubectl was called", joinArgs(tc.args))
		}
	}
}

// kx yaml parses kubectl get's flags; --show is kx's own, and is still
// refused beside an output format in any spelling.
func TestYamlParsesKubectlFlags(t *testing.T) {
	kube, err := indexCommand(t, newYamlCommand, "--show-managed-fields", "1")
	if err != nil {
		t.Fatalf("kx yaml --show-managed-fields 1: %v", err)
	}
	if len(kube.runs) != 1 || joinArgs(kube.runs[0]) != "get Pod nginx -n prod -o yaml --show-managed-fields" {
		t.Errorf("kubectl = %q, want the manifest with kubectl's --show-managed-fields", kube.runs)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--show", "spec", "-ojson", "1"}, "'--show' cannot be combined with an output format"},
		{[]string{"-n", "other", "1"}, "'--namespace' cannot be combined with an index"},
		{[]string{"1", "--bogus"}, "'--bogus' isn't a flag of kubectl get"},
		{[]string{"--show", "spec"}, "requires <index>"},
	} {
		kube, err := indexCommand(t, newYamlCommand, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("kx yaml %s: err = %v, want %q", joinArgs(tc.args), err, tc.want)
		}
		if len(kube.runs) > 0 {
			t.Errorf("kx yaml %s: kubectl was called: %q", joinArgs(tc.args), kube.runs)
		}
	}
}
