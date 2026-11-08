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
