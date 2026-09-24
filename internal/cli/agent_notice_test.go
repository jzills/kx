package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// These pin Task 2 of the agent-listing safety plan: a stderr notice a
// mutating command prints when the index it is about to spend was resolved
// out of a listing an MCP tool made on an agent's behalf — before the command
// acts, with no prompt, no refusal, and no change to stdout or the exit code.

// saveListing writes the current entry directly, as `kx get` would, tagged as
// the agent's when tagged is true.
func saveListing(t *testing.T, services Services, kind kinds.Kind, namespace string, tagged bool, names ...string) {
	t.Helper()
	entry := state.State{Resources: state.NewResources(names, kind), Namespace: namespace}
	if tagged {
		entry.Source = state.SourceMCP
	}
	if err := services.State.Save(entry); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// runCaptured runs cmd and returns its stdout and stderr separately, through
// the same render.SetOutput seam every other CLI test captures output with.
func runCaptured(t *testing.T, cmd *cobra.Command, args []string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	render.SetOutput(&out, &errOut, "github-dark")
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// withFakeKubernetes returns services with a Kubernetes client the tree,
// diagnostic and events commands can call without reaching a real cluster —
// empty, since these tests only care whether the index resolve prints (or
// doesn't print) a notice, not whether the resource is found afterward.
func withFakeKubernetes(services Services) Services {
	services.Kubernetes = func() (kubernetes.Interface, error) { return fake.NewSimpleClientset(), nil }
	return services
}

const noticeSubstring = "is from a kx mcp listing"

// mutatingCase is one of the ten commands Task 2's brief calls out, plus what
// it takes to exercise a single tagged index through its RunE.
type mutatingCase struct {
	name  string
	kind  kinds.Kind
	build func(services Services) *cobra.Command
	args  []string
	// output primes recordingKubectl.output with what this command's Kubectl
	// calls need to parse successfully; "" is fine for most.
	output string
}

func mutatingCommandCases() []mutatingCase {
	return []mutatingCase{
		{"scale", kinds.Deployment, newScaleCommand, []string{"1", "3"}, ""},
		{"rollout", kinds.Deployment, newRolloutCommand, []string{"restart", "1"}, ""},
		{"cordon", kinds.Node, func(s Services) *cobra.Command { return newCordonCommand(s, "cordon") }, []string{"1"}, ""},
		{"uncordon", kinds.Node, func(s Services) *cobra.Command { return newCordonCommand(s, "uncordon") }, []string{"1"}, ""},
		{"debug", kinds.Pod, newDebugCommand, []string{"1"}, ""},
		{"edit", kinds.Pod, newEditCommand, []string{"1"}, ""},
		{"exec", kinds.Pod, newExecCommand, []string{"1"}, ""},
		{"label", kinds.Pod, func(s Services) *cobra.Command {
			return newMetadataWriteCommand(s, "label", "labels", "", "")
		}, []string{"1", "env=prod"}, `{"metadata":{"labels":{}}}`},
		{"annotate", kinds.Pod, func(s Services) *cobra.Command {
			return newMetadataWriteCommand(s, "annotate", "annotations", "", "")
		}, []string{"1", "note=hi"}, `{"metadata":{"annotations":{}}}`},
		{"cp", kinds.Pod, newCopyCommand, []string{"1:/var/log/app.log", "./app.log"}, ""},
	}
}

// namespaceFor is the namespace a tagged listing of kind should carry: "" for
// a cluster-scoped kind (Node), "prod" otherwise — matching what a real
// listing of either shape would record.
func namespaceFor(kind kinds.Kind) string {
	if namespaced, known := kinds.Namespaced(kind); known && !namespaced {
		return ""
	}
	return "prod"
}

// installAgentIndexNotice must tolerate a Services{} literal with no State —
// several existing tests build a mutating command's cobra.Command that way to
// exercise pure argument validation before any index would be resolved, and
// this panicked the whole suite (TestScaleRejectsANonNumericReplicaCount)
// until installAgentIndexNotice learned to no-op on a nil services.State.
func TestInstallAgentIndexNoticeToleratesNilState(t *testing.T) {
	installAgentIndexNotice(Services{})
}

// (b) The command set: for each of the ten commands, resolving a tagged index
// prints the notice sentence to stderr, and stdout is byte-identical to the
// same command run over an untagged listing. The notice is printed exactly
// once — several of these commands resolve the same ref twice internally
// (once in RunE to check a scope flag, again inside the Execute method that
// acts), which is exactly the duplicate installAgentIndexNotice's dedup set
// exists to collapse.
func TestAgentIndexNoticeCommandSet(t *testing.T) {
	for _, tc := range mutatingCommandCases() {
		t.Run(tc.name, func(t *testing.T) {
			namespace := namespaceFor(tc.kind)

			tagged := switchServices(t, &recordingKubectl{output: tc.output})
			saveListing(t, tagged, tc.kind, namespace, true, "target")
			stdoutTagged, stderrTagged, err := runCaptured(t, tc.build(tagged), tc.args)
			if err != nil {
				t.Fatalf("tagged run: %v", err)
			}

			if got := strings.Count(stderrTagged, noticeSubstring); got != 1 {
				t.Fatalf("notice printed %d times in stderr, want exactly 1:\n%s", got, stderrTagged)
			}
			where := ""
			if namespace != "" {
				where = " in " + namespace
			}
			want := "Index 1 is from a kx mcp listing — " + string(tc.kind) + "/target" + where +
				". Run 'kx state' to see it."
			if !strings.Contains(stderrTagged, want) {
				t.Errorf("stderr = %q, want it to contain %q", stderrTagged, want)
			}

			untagged := switchServices(t, &recordingKubectl{output: tc.output})
			saveListing(t, untagged, tc.kind, namespace, false, "target")
			stdoutUntagged, stderrUntagged, err := runCaptured(t, tc.build(untagged), tc.args)
			if err != nil {
				t.Fatalf("untagged run: %v", err)
			}
			if stderrUntagged != "" {
				t.Errorf("untagged stderr = %q, want empty — an untagged listing gets no notice", stderrUntagged)
			}
			if stdoutTagged != stdoutUntagged {
				t.Errorf("stdout differs between the tagged and untagged run:\n tagged   %q\n untagged %q",
					stdoutTagged, stdoutUntagged)
			}
		})
	}
}

// (c) Read-only commands never print the notice, however their index
// resolves — they never call installAgentIndexNotice, so the hook stays nil
// on services.State the way a freshly built Service always has it.
func TestAgentIndexNoticeReadOnlyCommandsSilent(t *testing.T) {
	cases := []struct {
		name  string
		kind  kinds.Kind
		build func(services Services) *cobra.Command
		args  []string
	}{
		{"logs", kinds.Pod, newLogsCommand, []string{"1"}},
		{"describe", kinds.Pod, newDescribeCommand, []string{"1"}},
		{"yaml", kinds.Pod, newYamlCommand, []string{"1"}},
		{"events", kinds.Pod, newEventsCommand, []string{"1"}},
		{"tree", kinds.Pod, newTreeCommand, []string{"1"}},
		{"diagnostic", kinds.Pod, func(s Services) *cobra.Command {
			return newDiagnosticCommand(s, "diagnostic", []string{"diag"})
		}, []string{"1"}},
		{"labels", kinds.Pod, func(s Services) *cobra.Command {
			return newMetadataReadCommand(s, "labels", "", "", "labels", "LABEL", true)
		}, []string{"1"}},
		{"port-forward", kinds.Pod, newPortForwardCommand, []string{"1", "8080:80"}},
		{"ref", kinds.Pod, newRefCommand, []string{"1"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kube := &recordingKubectl{output: `{"metadata":{"labels":{}}}`}
			services := withFakeKubernetes(switchServices(t, kube))
			saveListing(t, services, tc.kind, "prod", true, "target")

			_, stderr, _ := runCaptured(t, tc.build(services), tc.args)
			// The command's own outcome is not the point here — some of these
			// error against the bare fake clientset once the index resolves —
			// only that resolving it never prints the notice.
			if strings.Contains(stderr, noticeSubstring) {
				t.Errorf("%s printed the agent-listing notice, but never installs the hook:\n%s",
					tc.name, stderr)
			}
		})
	}
}

// (d) Review Focus 3: kx cordon 1 2 over a listing with two tagged indexes
// prints two notice lines, not one and not zero.
func TestAgentIndexNoticeMultiIndexPrintsOnePerIndex(t *testing.T) {
	services := switchServices(t, &recordingKubectl{})
	saveListing(t, services, kinds.Node, "", true, "node-a", "node-b")

	_, stderr, err := runCaptured(t, newCordonCommand(services, "cordon"), []string{"1", "2"})
	if err != nil {
		t.Fatalf("cordon 1 2: %v", err)
	}

	if got := strings.Count(stderr, noticeSubstring); got != 2 {
		t.Fatalf("notice printed %d times, want exactly 2:\n%s", got, stderr)
	}
	wantA := "Index 1 is from a kx mcp listing — Node/node-a. Run 'kx state' to see it."
	wantB := "Index 2 is from a kx mcp listing — Node/node-b. Run 'kx state' to see it."
	if !strings.Contains(stderr, wantA) {
		t.Errorf("stderr = %q, want it to contain %q", stderr, wantA)
	}
	if !strings.Contains(stderr, wantB) {
		t.Errorf("stderr = %q, want it to contain %q", stderr, wantB)
	}
}

// (e) Review Focus 4: kx delete 3 without --yes gets the provenance suffix on
// the confirm prompt and no stderr notice; with --yes it gets the notice and
// no prompt. Drain is checked the same way.
func TestAgentIndexNoticeDeleteAndDrainOnlyWithYes(t *testing.T) {
	t.Run("delete", func(t *testing.T) {
		// kx delete's RunE wires DeleteCommand.Confirm to the package-level
		// render.Confirm directly rather than services.confirm() (unlike
		// drain, below) — an existing quirk, not something this task
		// changes — so the prompt is read from stdin (empty under go test,
		// which aborts) and asserted on the printed prompt text in stdout
		// rather than through a Services.Confirm override.
		t.Run("without --yes: prompt suffix, no notice", func(t *testing.T) {
			services := switchServices(t, &recordingKubectl{})
			saveListing(t, services, kinds.Pod, "prod", true, "a", "b", "c")

			stdout, stderr, err := runCaptured(t, newDeleteCommand(services), []string{"3"})
			var aborted render.ErrAborted
			if !errors.As(err, &aborted) {
				t.Fatalf("delete 3 (empty stdin): err = %v, want ErrAborted", err)
			}
			if want := "Delete Pod/c in prod — from a kx mcp listing?"; !strings.Contains(stdout, want) {
				t.Errorf("stdout = %q, want it to contain the prompt %q", stdout, want)
			}
			if strings.Contains(stderr, noticeSubstring) {
				t.Errorf("stderr = %q, want no notice — the prompt already named the provenance", stderr)
			}
		})

		t.Run("with --yes: notice, no prompt", func(t *testing.T) {
			services := switchServices(t, &recordingKubectl{})
			saveListing(t, services, kinds.Pod, "prod", true, "a", "b", "c")
			prompted := false
			services.Confirm = func(string) error { prompted = true; return nil }

			_, stderr, err := runCaptured(t, newDeleteCommand(services), []string{"3", "--yes"})
			if err != nil {
				t.Fatalf("delete 3 --yes: %v", err)
			}
			if prompted {
				t.Error("prompted despite --yes")
			}
			want := "Index 3 is from a kx mcp listing — Pod/c in prod. Run 'kx state' to see it."
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, want)
			}
			if got := strings.Count(stderr, noticeSubstring); got != 1 {
				t.Errorf("notice printed %d times, want exactly 1", got)
			}
		})
	})

	t.Run("drain", func(t *testing.T) {
		t.Run("without --yes: prompt suffix, no notice", func(t *testing.T) {
			services := switchServices(t, &recordingKubectl{})
			saveListing(t, services, kinds.Node, "", true, "node-a")
			var prompted string
			services.Confirm = func(m string) error { prompted = m; return nil }

			_, stderr, err := runCaptured(t, newDrainCommand(services), []string{"1"})
			if err != nil {
				t.Fatalf("drain 1: %v", err)
			}
			if want := "Evict all pods from Node/node-a — from a kx mcp listing?"; prompted != want {
				t.Errorf("prompt = %q, want %q", prompted, want)
			}
			if strings.Contains(stderr, noticeSubstring) {
				t.Errorf("stderr = %q, want no notice — the prompt already named the provenance", stderr)
			}
		})

		t.Run("with --yes: notice, no prompt", func(t *testing.T) {
			services := switchServices(t, &recordingKubectl{})
			saveListing(t, services, kinds.Node, "", true, "node-a")
			prompted := false
			services.Confirm = func(string) error { prompted = true; return nil }

			_, stderr, err := runCaptured(t, newDrainCommand(services), []string{"1", "--yes"})
			if err != nil {
				t.Fatalf("drain 1 --yes: %v", err)
			}
			if prompted {
				t.Error("prompted despite --yes")
			}
			want := "Index 1 is from a kx mcp listing — Node/node-a. Run 'kx state' to see it."
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, want)
			}
			if got := strings.Count(stderr, noticeSubstring); got != 1 {
				t.Errorf("notice printed %d times, want exactly 1", got)
			}
		})
	})
}

// (f) The pinning test: every command in the tree is accounted for by one of
// two explicit lists — the mutating ones, which must carry the kx.mutating
// annotation installAgentIndexNotice's callers set, and everything else,
// which must not. A command in neither list fails the test, so a new
// command — mutating or not — forces a decision rather than silently landing
// on either side.
func TestAgentIndexNoticeCommandAllowlistIsPinned(t *testing.T) {
	mutating := map[string]bool{
		"scale": true, "rollout": true, "cordon": true, "uncordon": true,
		"debug": true, "edit": true, "exec": true, "label": true,
		"annotate": true, "cp": true, "delete": true, "drain": true,
	}
	nonMutating := map[string]bool{
		"annotations": true, "completion": true, "context": true, "describe": true,
		"diagnostic": true, "engine": true, "events": true, "get": true,
		"labels": true, "logs": true, "mark": true, "mcp": true,
		"namespace": true, "port-forward": true, "ref": true, "scan": true,
		"secret": true, "state": true, "theme": true, "top": true,
		"tree": true, "unmark": true, "yaml": true,
	}

	root := NewRoot(NewServices(config.Default()), "test")
	seen := map[string]bool{}
	for _, cmd := range root.Commands() {
		name := cmd.Name()
		seen[name] = true
		wantMutating := mutating[name]
		gotMutating := cmd.Annotations[mutatingAnnotation] == "true"
		switch {
		case wantMutating && !gotMutating:
			t.Errorf("%s is on the mutating allowlist but carries no %s annotation",
				name, mutatingAnnotation)
		case !wantMutating && gotMutating:
			t.Errorf("%s carries %s but is not on the allowlist — "+
				"was it made mutating without updating the pin?", name, mutatingAnnotation)
		case !wantMutating && !nonMutating[name]:
			t.Errorf("%s is a command neither list accounts for — decide whether it needs "+
				"installAgentIndexNotice, then add it to the mutating or the read-only list", name)
		}
	}
	for name := range mutating {
		if !seen[name] {
			t.Errorf("%s is on the mutating allowlist but no longer exists as a command", name)
		}
	}
}
