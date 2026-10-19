package cli

import (
	"bytes"
	"errors"
	kube "github.com/jzills/kx/internal/kubectl"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
	"github.com/spf13/cobra"
)

func node(name string) fakeResolver {
	// A Node is cluster-scoped, so the entry carries no namespace.
	return fakeResolver{name: name, namespace: "", kind: kinds.Node}
}

// Cordon and uncordon are Node-only, with the message shape exec and cp
// already use.
func TestCordonRefusesANonNode(t *testing.T) {
	for _, verb := range []string{"cordon", "uncordon"} {
		command := NodeCommand{
			Kubectl: &recordingKubectl{}, State: workload("web", kinds.Deployment), Verb: verb,
		}
		_, err := command.Execute(state.Ref{Index: 1}, nil)
		if err == nil {
			t.Fatalf("%s: succeeded on a Deployment", verb)
		}
		// Both halves: the kind the index actually resolved to, and the kinds
		// the command does work on. Naming only the second left the user to
		// work out what index 1 had been, which is the one fact an index model
		// takes away from them.
		if !strings.Contains(err.Error(), "'Deployment'") ||
			!strings.Contains(err.Error(), "only Nodes") {
			t.Errorf("%s: err = %q, want it to name Deployment and Nodes", verb, err)
		}
	}
}

func TestCordonRunsKubectlAgainstTheNode(t *testing.T) {
	kubectl := &recordingKubectl{}
	command := NodeCommand{Kubectl: kubectl, State: node("node-a"), Verb: "cordon"}
	message, err := command.Execute(state.Ref{Index: 1}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(kubectl.runs) != 1 {
		t.Fatalf("kubectl runs = %v, want one", kubectl.runs)
	}
	want := []string{"cordon", "node-a"}
	if !slices.Equal(kubectl.runs[0], want) {
		t.Errorf("kubectl args = %v, want %v", kubectl.runs[0], want)
	}
	// No -n: a Node is cluster-scoped, and kubectl cordon takes no namespace.
	if slices.Contains(kubectl.runs[0], "-n") {
		t.Errorf("kubectl args = %v, want no namespace flag", kubectl.runs[0])
	}
	if !strings.Contains(message, "node-a") {
		t.Errorf("message = %q, want it to name the node", message)
	}
}

// A vanished node is reported as stale so the caller can relist, rather than
// as whatever kubectl said about a name that is no longer there.
func TestCordonReportsAVanishedNodeAsStale(t *testing.T) {
	kubectl := &recordingKubectl{err: kube.Error{Stderr: `Error from server (NotFound): nodes "node-a" not found`}, probeCode: 1}
	command := NodeCommand{Kubectl: kubectl, State: node("node-a"), Verb: "cordon"}
	_, err := command.Execute(state.Ref{Index: 1}, nil)

	var stale StaleResourceError
	if !errors.As(err, &stale) {
		t.Fatalf("err = %#v, want StaleResourceError", err)
	}
	if stale.Kind != kinds.Node {
		t.Errorf("stale kind = %q, want Node", stale.Kind)
	}
}

// Drain is single-index and confirms first. Declining must not run kubectl.
func TestDrainAbortsWithoutConfirmation(t *testing.T) {
	kubectl := &recordingKubectl{}
	declined := errors.New("aborted")
	command := DrainCommand{
		Kubectl: kubectl, State: node("node-a"),
		Confirm: func(string) error { return declined },
	}
	if err := command.Execute(state.Ref{Index: 1}, false, nil); !errors.Is(err, declined) {
		t.Fatalf("err = %v, want the confirmation's own error", err)
	}
	if len(kubectl.interactive) != 0 {
		t.Errorf("the drain ran despite a declined confirmation: %v", kubectl.interactive)
	}
	// One Run is expected and harmless: the existence preflight that turns a
	// vanished node into a stale-state error before the prompt asks about it.
	for _, run := range kubectl.runs {
		if !slices.Equal(run, []string{"get", "node", "node-a"}) {
			t.Errorf("kubectl ran %v despite a declined confirmation", run)
		}
	}
}

// --yes skips the prompt, which is what a script needs.
func TestDrainSkipsTheatPromptWithYes(t *testing.T) {
	kubectl := &recordingKubectl{}
	asked := false
	command := DrainCommand{
		Kubectl: kubectl, State: node("node-a"),
		Confirm: func(string) error { asked = true; return nil },
	}
	if err := command.Execute(state.Ref{Index: 1}, true, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if asked {
		t.Error("--yes still prompted")
	}
	if len(kubectl.interactive) != 1 {
		t.Fatalf("kubectl interactive = %v, want one drain", kubectl.interactive)
	}
}

// Drain streams rather than capturing: it can take minutes, and its progress is
// the only sign it is working.
func TestDrainStreamsAndForwardsFlags(t *testing.T) {
	kubectl := &recordingKubectl{}
	command := DrainCommand{
		Kubectl: kubectl, State: node("node-a"),
		Confirm: func(string) error { return nil },
	}
	if err := command.Execute(state.Ref{Index: 1}, true, []string{"--ignore-daemonsets", "--force"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	args := kubectl.interactive[0]
	if args[0] != "drain" || args[1] != "node-a" {
		t.Errorf("kubectl args = %v, want a drain of node-a", args)
	}
	for _, flag := range []string{"--ignore-daemonsets", "--force"} {
		if !slices.Contains(args, flag) {
			t.Errorf("kubectl args = %v, want %s forwarded", args, flag)
		}
	}
}

// The drain confirm prompt carries the same provenance suffix delete's does.
func TestDrainConfirmNamesAnMCPListing(t *testing.T) {
	kubectl := &recordingKubectl{}
	var prompted string
	command := DrainCommand{
		Kubectl: kubectl, State: sourcedResolver{fakeResolver: node("node-a"), source: "mcp"},
		Confirm: func(m string) error { prompted = m; return nil },
	}
	if err := command.Execute(state.Ref{Index: 1}, false, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if want := "Evict all pods from Node/node-a — from a kx mcp listing?"; prompted != want {
		t.Errorf("prompt = %q, want %q", prompted, want)
	}
}

func TestDrainConfirmOmitsProvenanceForAUserMadeListing(t *testing.T) {
	kubectl := &recordingKubectl{}
	var prompted string
	command := DrainCommand{
		Kubectl: kubectl, State: sourcedResolver{fakeResolver: node("node-a"), source: ""},
		Confirm: func(m string) error { prompted = m; return nil },
	}
	if err := command.Execute(state.Ref{Index: 1}, false, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if want := "Evict all pods from Node/node-a?"; prompted != want {
		t.Errorf("prompt = %q, want %q", prompted, want)
	}
}

// Drain has a kubectl round trip (the vanished-node preflight) between
// resolving its target and building the confirm prompt — exactly the window
// a second, independent read of the listing's Source used to race a
// concurrent kx mcp save in. Resolve panics on singleReadResolver, so this
// fails loudly if that second read is ever reintroduced.
func TestDrainResolvesTargetAndProvenanceInOneRead(t *testing.T) {
	kubectl := &recordingKubectl{}
	resolver := &singleReadResolver{fakeResolver: node("node-a"), source: state.SourceMCP}
	var prompted string
	command := DrainCommand{
		Kubectl: kubectl, State: resolver,
		Confirm: func(m string) error { prompted = m; return nil },
	}
	if err := command.Execute(state.Ref{Index: 1}, false, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resolver.resolveWithSourceCalls != 1 {
		t.Errorf("ResolveWithSource called %d times, want exactly 1", resolver.resolveWithSourceCalls)
	}
	if want := "Evict all pods from Node/node-a — from a kx mcp listing?"; prompted != want {
		t.Errorf("prompt = %q, want %q", prompted, want)
	}
}

func TestDrainRefusesANonNode(t *testing.T) {
	command := DrainCommand{
		Kubectl: &recordingKubectl{}, State: workload("web", kinds.Deployment),
		Confirm: func(string) error { return nil },
	}
	err := command.Execute(state.Ref{Index: 1}, true, nil)
	if err == nil || !strings.Contains(err.Error(), "'Deployment'") ||
		!strings.Contains(err.Error(), "only Nodes") {
		t.Errorf("err = %v, want it to name Deployment and Nodes", err)
	}
}

// A non-zero drain forwards kubectl's exit code rather than exiting 0 after
// kubectl has already printed why it failed.
func TestDrainForwardsAFailingExitCode(t *testing.T) {
	kubectl := &recordingKubectl{exitCode: 1, probeCode: 0}
	command := DrainCommand{
		Kubectl: kubectl, State: node("node-a"),
		Confirm: func(string) error { return nil },
	}
	err := command.Execute(state.Ref{Index: 1}, true, nil)

	var silent SilentError
	if !errors.As(err, &silent) || silent.Code != 1 {
		t.Errorf("err = %#v, want SilentError{1}", err)
	}
}

// A DisableFlagParsing command's arity check runs against the raw, unstripped
// argv, so `kx drain --help` used to be answered with an arity error instead of
// help — the trap that has now bitten kx cp and kx port-forward.
//
// This drives the full cobra dispatch (SetArgs + Execute) rather than RunE.
// RunE bypasses cobra's Args validator entirely, so a test calling it directly
// cannot catch this class of bug no matter how it is written.
func TestDrainHelpIsNotAnArityError(t *testing.T) {
	for _, argv := range [][]string{
		{"drain", "--help"},
		{"drain", "-h"},
	} {
		var out bytes.Buffer
		render.SetOutput(&out, &out, config.DefaultTheme)

		root := NewRoot(Services{}, "test")
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(argv)
		err := root.Execute()

		render.SetOutput(nil, nil, config.DefaultTheme)

		if err != nil {
			t.Fatalf("%v: Execute returned %v, want help", argv, err)
		}
		if !strings.Contains(out.String(), "--ignore-daemonsets") {
			t.Errorf("%v: help does not list the kubectl flags it forwards:\n%s", argv, out.String())
		}
	}
}

// kx drain parses kubectl's flags, so its arity gate counts positionals only:
// a forwarded flag can neither satisfy it nor overflow it. When kx split argv
// by hand the gate saw the raw argv, which is why there used to be none.
func TestDrainArityCountsOnlyPositionals(t *testing.T) {
	cmd := newDrainCommand(Services{})
	if err := cmd.ParseFlags([]string{"1", "--ignore-daemonsets", "--timeout", "1m"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := cmd.Args(cmd, cmd.Flags().Args()); err != nil {
		t.Errorf("kx drain 1 --ignore-daemonsets --timeout 1m failed the arity gate: %v", err)
	}
	cmd = newDrainCommand(Services{})
	if err := cmd.ParseFlags([]string{"--ignore-daemonsets", "--timeout", "1m"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := cmd.Args(cmd, cmd.Flags().Args()); err == nil {
		t.Error("kx drain with only flags passed the arity gate, want <index> required")
	}
}

// Every flag drain parses by hand must still be registered, or it works and
// vanishes from --help.
func TestDrainRegistersTheFlagsItParsesByHand(t *testing.T) {
	cmd := newDrainCommand(Services{})
	for _, name := range []string{
		"yes", "ignore-daemonsets", "delete-emptydir-data", "force",
		"grace-period", "timeout",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("--%s is not registered, so it is absent from --help", name)
		}
	}
}

// mixedListing saves a listing that straddles kinds, the way a tree listing or
// a hand-saved entry does — the shape `kx cordon 1..3` has to refuse whole.
func mixedListing(t *testing.T, resources ...state.Resource) *state.Service {
	t.Helper()
	store := &state.Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")}
	if err := store.Save(state.State{
		Resources: state.NewOrderedResources(resources),
	}); err != nil {
		t.Fatalf("prime state: %v", err)
	}
	return store
}

// resolveRefs resolves every index before a batch acts on any of them, and
// the kind is as much a precondition as the index resolving at all. The
// check used to live inside Execute, one index at a time, so `kx cordon 1..3`
// over a listing whose third entry is a Deployment cordoned the two nodes
// ahead of it, printed two successes and then failed.
func TestCordonRefusesAMixedBatchBeforeActingOnAnyOfIt(t *testing.T) {
	for _, verb := range []string{"cordon", "uncordon"} {
		kubectl := &recordingKubectl{}
		cmd := newCordonCommand(Services{
			Kubectl: kubectl,
			State: mixedListing(t,
				state.Resource{Name: "node-a", Kind: kinds.Node},
				state.Resource{Name: "node-b", Kind: kinds.Node},
				state.Resource{Name: "web", Kind: kinds.Deployment}),
		}, verb)

		err := cmd.RunE(cmd, []string{"1..3"})
		if err == nil {
			t.Fatalf("%s: a batch containing a Deployment was accepted", verb)
		}
		// Both halves: the kind the index actually resolved to, and the kinds
		// the command does work on. Naming only the second left the user to
		// work out what index 1 had been, which is the one fact an index model
		// takes away from them.
		if !strings.Contains(err.Error(), "'Deployment'") ||
			!strings.Contains(err.Error(), "only Nodes") {
			t.Errorf("%s: err = %q, want it to name Deployment and Nodes", verb, err)
		}
		if len(kubectl.runs) != 0 {
			t.Errorf("%s: ran %v — the batch must not half-apply", verb, kubectl.runs)
		}
	}
}

// The batch still runs when every index is a Node, so the guard above is not
// simply refusing everything.
func TestCordonAppliesAWholeNodeBatch(t *testing.T) {
	quietRender(t)
	kubectl := &recordingKubectl{}
	cmd := newCordonCommand(Services{
		Kubectl: kubectl,
		State: mixedListing(t,
			state.Resource{Name: "node-a", Kind: kinds.Node},
			state.Resource{Name: "node-b", Kind: kinds.Node}),
	}, "cordon")

	if err := cmd.RunE(cmd, []string{"1..2"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if len(kubectl.runs) != 2 {
		t.Errorf("kubectl ran %d times, want 2: %v", len(kubectl.runs), kubectl.runs)
	}
}

// Drain streams rather than captures, so kubectl's own "not found" reaches the
// terminal and only an exit code comes back — which withRefresh does not treat
// as stale. Drain was therefore the one node command that could not relist,
// and it asked "Evict all pods from Node/node-a?" about a node that had not
// existed for a while before saying so.
func TestDrainReportsAVanishedNodeAsStale(t *testing.T) {
	kubectl := &recordingKubectl{err: kube.Error{Stderr: `Error from server (NotFound): nodes "node-a" not found`}}
	asked := false
	command := DrainCommand{
		Kubectl: kubectl, State: node("node-a"),
		Confirm: func(string) error { asked = true; return nil },
	}

	var stale StaleResourceError
	err := command.Execute(state.Ref{Index: 1}, false, nil)
	if !errors.As(err, &stale) {
		t.Fatalf("err = %#v, want StaleResourceError", err)
	}
	if stale.Kind != kinds.Node {
		t.Errorf("stale kind = %q, want Node", stale.Kind)
	}
	if asked {
		t.Error("prompted for consent to drain a node that no longer exists")
	}
	if len(kubectl.interactive) != 0 {
		t.Errorf("ran the drain anyway: %v", kubectl.interactive)
	}
}

// A preflight that fails for any other reason must not take the command away:
// the drain runs and reports for itself.
func TestDrainProceedsWhenThePreflightFailsForAnotherReason(t *testing.T) {
	kubectl := &recordingKubectl{err: errors.New("the server was unable to return a response")}
	command := DrainCommand{
		Kubectl: kubectl, State: node("node-a"),
		Confirm: func(string) error { return nil },
	}
	if err := command.Execute(state.Ref{Index: 1}, true, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(kubectl.interactive) != 1 {
		t.Errorf("drain ran %d times, want 1: %v", len(kubectl.interactive), kubectl.interactive)
	}
}

// An out-of-range index in the batch must cordon nothing. kx delete has
// guaranteed this since it was written; cordon parsed without validating, so
// it cordoned node 1 and then reported the failure — leaving the cluster in a
// state the command had not been asked for.
func TestCordonValidatesEveryIndexBeforeCordoningAny(t *testing.T) {
	kube := &recordingKubectl{}
	services := switchServices(t, kube)
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"node-a"}, kinds.Node),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cmd := newCordonCommand(services, "cordon")
	cmd.SetArgs([]string{"1", "99"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("cordon succeeded despite an out-of-range index in the batch")
	}
	if len(kube.runs) != 0 {
		t.Errorf("kubectl was called %d times, want 0 — index 99 is out of range and "+
			"should be caught before node 1 is cordoned", len(kube.runs))
	}
}

// kx drain parses kubectl drain's flags: the index may follow them, and -y is
// kx's wherever it sits.
func TestDrainParsesKubectlFlags(t *testing.T) {
	kube, err := indexCommandOn(t, kinds.Node, newDrainCommand, "--ignore-daemonsets", "-y", "1", "--grace-period", "-1")
	if err != nil {
		t.Fatalf("kx drain --ignore-daemonsets -y 1 --grace-period -1: %v", err)
	}
	// The node is looked up first; the drain is the last call.
	calls := append(kube.runs, kube.interactive...)
	if len(calls) == 0 || joinArgs(calls[len(calls)-1]) != "drain nginx --ignore-daemonsets --grace-period=-1" {
		t.Errorf("kubectl = %q, want the drain with both flags last", calls)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"1", "-y", "--bogus"}, "'--bogus' isn't a flag of kubectl drain"},
		{[]string{"-y", "--force"}, "requires <index>"},
	} {
		kube, err := indexCommandOn(t, kinds.Node, newDrainCommand, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("kx drain %s: err = %v, want %q", joinArgs(tc.args), err, tc.want)
		}
		if len(kube.runs)+len(kube.interactive) > 0 {
			t.Errorf("kx drain %s: kubectl was called", joinArgs(tc.args))
		}
	}
}

// kx cordon and uncordon parse their kubectl flags from the table, as drain
// does: before, cobra refused --dry-run with a bare "unknown flag", and -- had
// no way through. Every node in the batch gets the same flags.
func TestCordonParsesKubectlFlags(t *testing.T) {
	for _, verb := range []string{"cordon", "uncordon"} {
		build := func(s Services) *cobra.Command { return newCordonCommand(s, verb) }
		kube, err := indexCommandOn(t, kinds.Node, build, "--dry-run=server", "1", "2")
		if err != nil {
			t.Fatalf("kx %s --dry-run=server 1 2: %v", verb, err)
		}
		want := []string{verb + " nginx --dry-run=server", verb + " redis --dry-run=server"}
		if got := []string{}; len(kube.runs) != 2 ||
			!slices.Equal(append(got, joinArgs(kube.runs[0]), joinArgs(kube.runs[1])), want) {
			t.Errorf("kx %s: kubectl = %q, want %q", verb, kube.runs, want)
		}
		for _, tc := range []struct {
			args []string
			want string
		}{
			{[]string{"1", "--bogus"}, "'--bogus' isn't a flag of kubectl " + verb},
			{[]string{"1", "node-b"}, "'node-b' isn't an argument of"},
			{[]string{"--dry-run=server"}, "requires <index>"},
			{[]string{"1", "--context", "other"}, "'--context'"},
		} {
			kube, err := indexCommandOn(t, kinds.Node, build, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("kx %s %s: err = %v, want %q", verb, joinArgs(tc.args), err, tc.want)
			}
			if len(kube.runs)+len(kube.interactive) > 0 {
				t.Errorf("kx %s %s: kubectl was called: %q", verb, joinArgs(tc.args), kube.runs)
			}
		}
	}
}

// kx's line replaces kubectl's "(server dry run)", so a dry run has to say so
// itself; --dry-run=none is a real cordon and claims nothing.
func TestCordonDryRunSaysNothingChanged(t *testing.T) {
	for _, tc := range []struct {
		extra  []string
		dryRun bool
	}{
		{[]string{"--dry-run=server"}, true},
		{[]string{"--dry-run"}, true},
		{[]string{"--dry-run=none"}, false},
		{nil, false},
	} {
		command := NodeCommand{Kubectl: &recordingKubectl{}, State: node("node-a"), Verb: "uncordon"}
		message, err := command.Execute(state.Ref{Index: 1}, tc.extra)
		if err != nil {
			t.Fatalf("Execute(%q): %v", tc.extra, err)
		}
		if got := strings.Contains(message, "(dry run — nothing was changed)"); got != tc.dryRun {
			t.Errorf("Execute(%q) = %q, want dry run labelled: %v", tc.extra, message, tc.dryRun)
		}
	}
}

// --yes is kx's, parsed by cobra wherever it sits: with it the prompt is never
// asked; without it a declined prompt drains nothing.
func TestDrainYesSkipsThePromptWhereverItSits(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		drains bool
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
			Resources: state.NewResources([]string{"node-a"}, kinds.Node),
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		quietRender(t)
		_ = executeArgv(newDrainCommand(services), tc.args)
		if drained := len(kube.interactive) > 0; drained != tc.drains || asked == tc.drains {
			t.Errorf("kx drain %s: drained=%v asked=%v, want drained=%v", joinArgs(tc.args), drained, asked, tc.drains)
		}
	}
}
