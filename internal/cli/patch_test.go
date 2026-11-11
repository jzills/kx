package cli

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// The patch reaches kubectl exactly as typed, its type and value attached to
// their flags, after the kind, name and namespace kx resolved from the index.
func TestPatchForwardsThePatchAfterTheResolvedTarget(t *testing.T) {
	kube, err := indexCommandOn(t, kinds.Deployment, newPatchCommand,
		"1", "--type", "merge", "-p", `{"spec":{"replicas":2}}`)
	if err != nil {
		t.Fatalf("kx patch 1 --type merge -p ...: %v", err)
	}
	if len(kube.runs) != 1 {
		t.Fatalf("kubectl ran %d times, want once: %q", len(kube.runs), kube.runs)
	}
	want := `patch Deployment/nginx -n prod --type=merge --patch={"spec":{"replicas":2}}`
	if got := joinArgs(kube.runs[0]); got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

// Nothing reaches kubectl when the invocation is wrong: a flag kubectl patch
// doesn't have, an index beside -n, no index at all.
func TestPatchRefusesBeforeKubectlRuns(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"1", "--bogus"}, "'--bogus' isn't a flag of kubectl patch"},
		{[]string{"-n", "other", "1", "-p", "{}"}, "'--namespace' cannot be combined with an index"},
		{[]string{"-p", "{}"}, "requires <index>"},
		// Each names the resource to patch, which the index already does;
		// kubectl refuses every one beside a name.
		{[]string{"1", "-f", "cm.yaml", "-p", "{}"},
			"'--filename' cannot be combined with an index — it names the resource to patch, which the index already does."},
		{[]string{"1", "-k", "overlay", "-p", "{}"}, "'--kustomize' cannot be combined with an index"},
		{[]string{"1", "--local", "-p", "{}"}, "'--local' cannot be combined with an index"},
		{[]string{"1", "-R", "-p", "{}"}, "'--recursive' cannot be combined with an index"},
	} {
		kube, err := indexCommandOn(t, kinds.Deployment, newPatchCommand, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("kx patch %s: err = %v, want %q", joinArgs(tc.args), err, tc.want)
		}
		if len(kube.runs) > 0 {
			t.Errorf("kx patch %s: kubectl was called: %q", joinArgs(tc.args), kube.runs)
		}
	}
}

// kx prints its own line in place of kubectl's, so the line has to carry the
// two things kubectl's reply would have said: that a dry run changed nothing,
// and that a patch already matching the resource changed nothing either.
// Without them, both read "✓ Patched Deployment/api" — a change that never
// happened.
func TestPatchSaysWhenNothingChanged(t *testing.T) {
	for _, tc := range []struct {
		reply string
		args  []string
		want  string
	}{
		{"deployment.apps/api patched", nil, "Patched Deployment/api"},
		{"deployment.apps/api patched (no change)", nil,
			"Patched Deployment/api (no change — it already matched)"},
		{"deployment.apps/api patched (server dry run)", []string{"--dry-run=server"},
			"Patched Deployment/api (dry run — nothing was changed)"},
	} {
		message, _, err := PatchCommand{
			Kubectl: &recordingKubectl{output: tc.reply}, State: workload("api", kinds.Deployment),
		}.Execute(state.Ref{Index: 1}, tc.args)
		if err != nil {
			t.Fatalf("Execute(%q): %v", tc.reply, err)
		}
		if message != tc.want {
			t.Errorf("message for %q = %q, want %q", tc.reply, message, tc.want)
		}
	}
}

// --local=false is the flag's own default and names nothing, so it is read as
// kubectl reads a boolean and reaches kubectl. The control for the refusals
// above: they are about the flags' meaning, not their spelling.
func TestPatchPassesASourceFlagThatSelectsNothing(t *testing.T) {
	kube, err := indexCommandOn(t, kinds.Deployment, newPatchCommand, "1", "--local=false", "-p", "{}")
	if err != nil {
		t.Fatalf("kx patch 1 --local=false: %v", err)
	}
	if len(kube.runs) != 1 {
		t.Errorf("kubectl ran %d times, want once", len(kube.runs))
	}
}

// The refused flags are not offered: help and completion leave them out.
func TestPatchHidesTheFlagsItRefuses(t *testing.T) {
	cmd := newPatchCommand(argvServices(t))
	for _, name := range patchSourceFlags {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			t.Fatalf("--%s is not registered, so it would be refused as unknown, not named", name)
		}
		if !flag.Hidden {
			t.Errorf("--%s is offered in help, though kx patch refuses it", name)
		}
	}
}

// With -o kubectl prints the object, not its summary line, and the object
// may hold any text. A field reading "(no change)" made kx report a real
// change as a no-op.
func TestPatchReadsNoChangeFromTheSummaryLineAlone(t *testing.T) {
	object := "apiVersion: v1\nkind: ConfigMap\ndata:\n  note: patched (no change)\n"
	message, _, err := PatchCommand{
		Kubectl: &recordingKubectl{output: object}, State: workload("x", kinds.ConfigMap),
	}.Execute(state.Ref{Index: 1}, []string{"--output=yaml"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if want := "Patched ConfigMap/x"; message != want {
		t.Errorf("message = %q, want %q", message, want)
	}
}
