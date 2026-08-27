package cli

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
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
		_, err := command.Execute(1)
		if err == nil {
			t.Fatalf("%s: succeeded on a Deployment", verb)
		}
		if !strings.Contains(err.Error(), verb+" is only supported for nodes") {
			t.Errorf("%s: err = %q, want a nodes-only message", verb, err)
		}
	}
}

func TestCordonRunsKubectlAgainstTheNode(t *testing.T) {
	kubectl := &recordingKubectl{}
	command := NodeCommand{Kubectl: kubectl, State: node("node-a"), Verb: "cordon"}
	message, err := command.Execute(1)
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
	kubectl := &recordingKubectl{err: errors.New(`Error from server (NotFound): nodes "node-a" not found`), probeCode: 1}
	command := NodeCommand{Kubectl: kubectl, State: node("node-a"), Verb: "cordon"}
	_, err := command.Execute(1)

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
	if err := command.Execute(1, false, nil); !errors.Is(err, declined) {
		t.Fatalf("err = %v, want the confirmation's own error", err)
	}
	if len(kubectl.interactive) != 0 || len(kubectl.runs) != 0 {
		t.Errorf("kubectl was called despite a declined confirmation: %v %v",
			kubectl.runs, kubectl.interactive)
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
	if err := command.Execute(1, true, nil); err != nil {
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
	if err := command.Execute(1, true, []string{"--ignore-daemonsets", "--force"}); err != nil {
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

func TestDrainRefusesANonNode(t *testing.T) {
	command := DrainCommand{
		Kubectl: &recordingKubectl{}, State: workload("web", kinds.Deployment),
		Confirm: func(string) error { return nil },
	}
	err := command.Execute(1, true, nil)
	if err == nil || !strings.Contains(err.Error(), "drain is only supported for nodes") {
		t.Errorf("err = %v, want a nodes-only message", err)
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
	err := command.Execute(1, true, nil)

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

// The arity gate must not exist at all, which is the only assertion that holds
// for every shape of the bug.
//
// Driving `drain --help` catches a MinimumNArgs(2) — the shape that bit kx cp —
// but not an ExactArgs(1), because "--help" is itself one argument and satisfies
// it. `drain 1 --ignore-daemonsets` would catch that one and not the first.
// Since a DisableFlagParsing command counts forwarded kubectl flags as
// positional arguments, no arity a validator could express is correct here, and
// asserting its absence is what covers both.
func TestDrainHasNoArityValidator(t *testing.T) {
	if newDrainCommand(Services{}).Args != nil {
		t.Error("drain has an Args validator; cobra runs it against the raw argv, " +
			"which counts forwarded kubectl flags as positional arguments")
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
