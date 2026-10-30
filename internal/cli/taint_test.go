package cli

import (
	"errors"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/state"
)

// kx taint builds kubectl taint's argv for the indexed Node: the taints to
// set as given, each --remove with kubectl's trailing '-', and kubectl's own
// flags last. --all=false selects no Node of its own, so it is forwarded, not
// refused as --all is.
func TestTaintBuildsKubectlTaintForTheIndexedNode(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"1", "dedicated=gpu:NoSchedule"}, "taint node nginx dedicated=gpu:NoSchedule"},
		{[]string{"2", "maintenance:NoExecute", "--remove", "dedicated"},
			"taint node redis maintenance:NoExecute dedicated-"},
		{[]string{"1", "--remove", "dedicated:NoSchedule", "--remove", "spot"},
			"taint node nginx dedicated:NoSchedule- spot-"},
		{[]string{"--overwrite", "1", "dedicated=infra:PreferNoSchedule", "--dry-run=server"},
			"taint node nginx dedicated=infra:PreferNoSchedule --overwrite --dry-run=server"},
		{[]string{"1", "dedicated=gpu:NoSchedule", "--all=false"},
			"taint node nginx dedicated=gpu:NoSchedule --all=false"},
	} {
		kube, err := indexCommandOn(t, kinds.Node, newTaintCommand, tc.args...)
		if err != nil {
			t.Errorf("kx taint %s: %v", joinArgs(tc.args), err)
			continue
		}
		if len(kube.runs) != 1 || joinArgs(kube.runs[0]) != tc.want {
			t.Errorf("kx taint %s ran %q, want [%s]", joinArgs(tc.args), kube.runs, tc.want)
		}
	}
}

// What kx taint refuses, it refuses before kubectl runs: a malformed taint
// or effect, kubectl's own removal spelling, a flag that would taint more
// than the indexed Node — after '--' as well — and an index that is not a
// Node.
func TestTaintRefusesBeforeKubectlRuns(t *testing.T) {
	for _, tc := range []struct {
		kind kinds.Kind
		args []string
		want string
	}{
		{kinds.Node, []string{"1"},
			"kx taint needs key=value:Effect to set, or --remove to name a taint to drop — neither was given."},
		// The kind is answered before the missing taint, so a Pod index is
		// not asked for a taint it could never be given.
		{kinds.Pod, []string{"1"},
			"kx taint does not support 'Pod' — only Nodes."},
		{kinds.Node, []string{"1", "dedicated=gpu"},
			"'dedicated=gpu' is not a taint — write key=value:Effect or key:Effect, as in 'dedicated=gpu:NoSchedule'."},
		{kinds.Node, []string{"1", "=gpu:NoSchedule"},
			"'=gpu:NoSchedule' is not a taint — write key=value:Effect or key:Effect, as in 'dedicated=gpu:NoSchedule'."},
		{kinds.Node, []string{"1", "dedicated=gpu:NoSched"},
			"Invalid value for the effect in 'dedicated=gpu:NoSched': 'NoSched'. Accepted values: NoSchedule, PreferNoSchedule, NoExecute."},
		{kinds.Node, []string{"1", "dedicated-"},
			"'dedicated-' is kubectl's spelling for removing a taint — use '--remove dedicated' instead."},
		{kinds.Node, []string{"1", "--remove", "dedicated-"},
			"Invalid value for '--remove': 'dedicated-' ends in kubectl's removal '-' — '--remove' adds it, so give the key as 'dedicated'."},
		{kinds.Node, []string{"1", "--remove", "dedicated-:NoSchedule"},
			"Invalid value for '--remove': 'dedicated-:NoSchedule' ends in kubectl's removal '-' — '--remove' adds it, so give the key as 'dedicated'."},
		{kinds.Node, []string{"1", "--remove", "dedicated=gpu"},
			"Invalid value for '--remove': 'dedicated=gpu' is not a valid taint key — give the key, or key:Effect."},
		{kinds.Node, []string{"1", "--remove", "dedicated:Never"},
			"Invalid value for the effect in 'dedicated:Never': 'Never'. Accepted values: NoSchedule, PreferNoSchedule, NoExecute."},
		{kinds.Node, []string{"1", "dedicated=gpu:NoSchedule", "--all"},
			"'--all' cannot be combined with an index — kubectl taint would apply it to every Node it selects, not only the indexed one."},
		{kinds.Node, []string{"1", "dedicated=gpu:NoSchedule", "--all=true"},
			"'--all' cannot be combined with an index — kubectl taint would apply it to every Node it selects, not only the indexed one."},
		{kinds.Node, []string{"1", "dedicated=gpu:NoSchedule", "-l", "pool=gpu"},
			"'--selector' cannot be combined with an index — kubectl taint would apply it to every Node it selects, not only the indexed one."},
		{kinds.Node, []string{"1", "dedicated=gpu:NoSchedule", "--", "--all"},
			"'--all' cannot be combined with an index — kubectl taint would apply it to every Node it selects, not only the indexed one."},
		{kinds.Pod, []string{"1", "dedicated=gpu:NoSchedule"},
			"kx taint does not support 'Pod' — only Nodes."},
	} {
		kube, err := indexCommandOn(t, tc.kind, newTaintCommand, tc.args...)
		if err == nil || err.Error() != tc.want {
			t.Errorf("kx taint %s: err = %v,\n want %q", joinArgs(tc.args), err, tc.want)
		}
		if len(kube.runs) > 0 {
			t.Errorf("kx taint %s ran kubectl %q", joinArgs(tc.args), kube.runs)
		}
	}
}

// kubectl answers a --remove of a taint the Node does not have with `taint
// "spot" not found`, the words IsNotFound reads as a vanished resource. That
// must reach the user as kubectl's error, not as a stale listing to refresh —
// neither from Execute nor from withRefresh's second look (isStale), which
// relisted the Nodes over it; only a Node that is really gone is stale.
//
// Which Node is gone is decided by a second look, and only a NotFound from
// it counts: an API server that cannot be reached fails the taint and the
// look alike, and was reported as a Node that no longer exists.
func TestTaintReportsAnAbsentTaintAsKubectlsErrorNotAStaleNode(t *testing.T) {
	absent := kubectl.Error{Stderr: `error: taint "spot" not found`}
	missing := kubectl.Error{Stderr: `Error from server (NotFound): nodes "node-a" not found`}
	unreachable := kubectl.Error{
		Stderr: "The connection to the server 127.0.0.1:6443 was refused — did you specify the right host or port?",
	}
	// One --remove of a taint against node-a, run three times over: errs
	// answers the taint and the second look in turn, so each case differs
	// only in what kubectl says to each.
	taint := func(kube *recordingKubectl, ref state.Ref) error {
		t.Helper()
		_, _, err := TaintCommand{Kubectl: kube}.
			Execute(Resolved{Ref: ref, Name: "node-a", Kind: kinds.Node}, nil, []string{"spot"}, nil, false, "")
		return err
	}

	// The taint fails, the Node is there: kubectl's own words, not a refresh.
	var stale StaleResourceError
	err := taint(&recordingKubectl{errs: []error{absent, nil}}, state.Ref{Index: 1})
	if errors.As(err, &stale) {
		t.Fatalf("err = %v, want kubectl's own error — the Node is still there", err)
	}
	if err == nil || err.Error() != absent.Stderr {
		t.Errorf("err = %v, want kubectl's %q", err, absent.Stderr)
	}
	if isStale(err) {
		t.Errorf("isStale(%v) = true — withRefresh would relist the Nodes over a taint that was never there", err)
	}

	// The control: the same failure on a Node the second look cannot find.
	err = taint(&recordingKubectl{errs: []error{absent, missing}}, state.Ref{Mark: "worker"})
	if !errors.As(err, &stale) {
		t.Fatalf("err = %v, want a StaleResourceError for a Node that is gone", err)
	}
	if stale.Ref.Mark != "worker" {
		t.Errorf("Ref.Mark = %q, want %q — the mark was lost building the stale error", stale.Ref.Mark, "worker")
	}

	// A cluster that cannot be reached answers neither call, and saying the
	// Node is gone would send the user to relist against the same cluster.
	err = taint(&recordingKubectl{errs: []error{unreachable, unreachable}}, state.Ref{Index: 1})
	if errors.As(err, &stale) {
		t.Fatalf("err = %v, want kubectl's connection error — the cluster is unreachable, not the Node missing", err)
	}
	if err == nil || err.Error() != unreachable.Stderr {
		t.Errorf("err = %v, want kubectl's %q", err, unreachable.Stderr)
	}
}

// kx's line stands in for kubectl's, so it counts what changed and says when
// a dry run changed nothing.
func TestTaintReportsWhatChanged(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"1", "dedicated=gpu:NoSchedule", "--remove", "spot"}, "✓ Tainted Node/nginx (set 1, removed 1)\n"},
		{[]string{"1", "--remove", "spot", "--dry-run=server"},
			"✓ Tainted Node/nginx (removed 1, dry run — nothing was changed)\n"},
	} {
		kube := &recordingKubectl{output: "node/nginx tainted"}
		services := switchServices(t, kube)
		if err := services.State.Save(state.State{
			Resources: state.NewResources([]string{"nginx"}, kinds.Node),
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		out := captureRender(t)
		if err := executeArgv(newTaintCommand(services), tc.args); err != nil {
			t.Fatalf("kx taint %s: %v", joinArgs(tc.args), err)
		}
		if out.String() != tc.want {
			t.Errorf("kx taint %s printed %q, want %q", joinArgs(tc.args), out.String(), tc.want)
		}
	}
}

// A NoExecute taint evicts every Pod that does not tolerate it, so it is
// asked about the way kx drain's eviction is. The other two effects evict
// nothing and are set without a word, a dry run changes nothing to ask
// about, and --yes skips the question as it does for drain.
func TestTaintConfirmsOnlyAnEvictingTaint(t *testing.T) {
	node := Resolved{Ref: state.Ref{Index: 1}, Name: "node-a", Kind: kinds.Node}
	for _, tc := range []struct {
		name   string
		sets   []string
		extra  []string
		yes    bool
		prompt string
	}{
		{"NoExecute asks", []string{"maintenance:NoExecute"}, nil, false,
			"Evict every Pod on Node/node-a that does not tolerate 'maintenance:NoExecute'?"},
		{"several are named together", []string{"a=1:NoExecute", "b=2:NoExecute"}, nil, false,
			"Evict every Pod on Node/node-a that does not tolerate 'a=1:NoExecute', 'b=2:NoExecute'?"},
		{"NoSchedule does not ask", []string{"dedicated=gpu:NoSchedule"}, nil, false, ""},
		{"PreferNoSchedule does not ask", []string{"dedicated=gpu:PreferNoSchedule"}, nil, false, ""},
		{"a removal does not ask", nil, nil, false, ""},
		{"--yes skips the question", []string{"maintenance:NoExecute"}, nil, true, ""},
		{"a dry run has nothing to ask about", []string{"maintenance:NoExecute"},
			[]string{"--dry-run=server"}, false, ""},
	} {
		kube := &recordingKubectl{output: "node/node-a tainted"}
		var prompted string
		removes := []string{}
		if len(tc.sets) == 0 {
			removes = []string{"spot"}
		}
		_, _, err := TaintCommand{
			Kubectl: kube, Confirm: func(m string) error { prompted = m; return nil },
		}.Execute(node, tc.sets, removes, tc.extra, tc.yes, "")
		if err != nil {
			t.Errorf("%s: Execute: %v", tc.name, err)
			continue
		}
		if prompted != tc.prompt {
			t.Errorf("%s: prompt = %q, want %q", tc.name, prompted, tc.prompt)
		}
		if len(kube.runs) != 1 {
			t.Errorf("%s: kubectl ran %v, want the taint once", tc.name, kube.runs)
		}
	}
}

// Declining the prompt stops the taint, so the Node is left alone, and the
// prompt names where the index came from as drain's does.
func TestTaintDeclinedLeavesTheNodeAloneAndNamesAnMCPListing(t *testing.T) {
	declined := errors.New("aborted")
	kube := &recordingKubectl{}
	_, _, err := TaintCommand{
		Kubectl: kube, Confirm: func(string) error { return declined },
	}.Execute(Resolved{Ref: state.Ref{Index: 1}, Name: "node-a", Kind: kinds.Node},
		[]string{"maintenance:NoExecute"}, nil, nil, false, "")
	if !errors.Is(err, declined) {
		t.Fatalf("err = %v, want the decline", err)
	}
	if len(kube.runs) != 0 {
		t.Errorf("kubectl ran %v after the prompt was declined", kube.runs)
	}

	var prompted string
	if _, _, err := (TaintCommand{
		Kubectl: &recordingKubectl{output: "ok"},
		Confirm: func(m string) error { prompted = m; return nil },
	}).Execute(Resolved{Ref: state.Ref{Index: 1}, Name: "node-a", Kind: kinds.Node},
		[]string{"maintenance:NoExecute"}, nil, nil, false, state.SourceMCP); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := "Evict every Pod on Node/node-a that does not tolerate 'maintenance:NoExecute' — from a kx mcp listing?"
	if prompted != want {
		t.Errorf("prompt = %q, want %q", prompted, want)
	}
}
