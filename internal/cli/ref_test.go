package cli

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// indexedResolver resolves each index to its own resource, which fakeResolver
// cannot: a spanning listing's whole point is that index 1 and index 2 are in
// different namespaces.
type indexedResolver struct {
	entries []struct {
		name, namespace string
		kind            kinds.Kind
	}
}

func (r indexedResolver) Fields(index int) (string, string, kinds.Kind, error) {
	entry := r.entries[index-1]
	return entry.name, entry.namespace, entry.kind, nil
}

func (r indexedResolver) Count() (int, error) { return len(r.entries), nil }

func refOf(entries ...[3]string) indexedResolver {
	var resolver indexedResolver
	for _, entry := range entries {
		resolver.entries = append(resolver.entries, struct {
			name, namespace string
			kind            kinds.Kind
		}{entry[0], entry[1], kinds.Kind(entry[2])})
	}
	return resolver
}

// The default form is a kubectl argument fragment, so `kubectl exec $(kx ref 3)`
// works without the caller assembling anything.
func TestRefPrintsAKubectlFragment(t *testing.T) {
	command := RefCommand{State: refOf([3]string{"web-abc", "diagnostics", "Pod"})}

	lines, err := command.Execute([]int{1}, "")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("lines = %v, want one", lines)
	}
	if lines[0] != "pod/web-abc -n diagnostics" {
		t.Errorf("line = %q, want %q", lines[0], "pod/web-abc -n diagnostics")
	}
}

// The kind is lowercased from the canonical name rather than spelled as
// kubectl's shorthand: `rs` and `deploy` are kubectl's own, and the point of
// this command is composing with tools that are not kubectl.
func TestRefLowercasesTheCanonicalKind(t *testing.T) {
	command := RefCommand{State: refOf(
		[3]string{"web", "prod", "Deployment"},
		[3]string{"web-abc", "prod", "ReplicaSet"},
		[3]string{"widget-1", "prod", "widgets.example.com"},
	)}

	lines, err := command.Execute([]int{1, 2, 3}, "")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for i, want := range []string{
		"deployment/web -n prod",
		"replicaset/web-abc -n prod",
		"widgets.example.com/widget-1 -n prod",
	} {
		if lines[i] != want {
			t.Errorf("line %d = %q, want %q", i+1, lines[i], want)
		}
	}
}

// A cluster-scoped resource gets no -n. The flag is wrong there, not merely
// redundant — kubectl takes it, and then the reference means something else.
func TestRefOmitsTheNamespaceForAClusterScopedResource(t *testing.T) {
	command := RefCommand{State: refOf([3]string{"desktop-control-plane", "", "Node"})}

	lines, err := command.Execute([]int{1}, "")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if lines[0] != "node/desktop-control-plane" {
		t.Errorf("line = %q, want no -n clause", lines[0])
	}
}

// One line per index, each complete and independent, so a spanning listing
// carries its own namespace on every line.
func TestRefPrintsOneCompleteLinePerIndex(t *testing.T) {
	command := RefCommand{State: refOf(
		[3]string{"waypoint", "default", "Pod"},
		[3]string{"istiod", "istio-system", "Pod"},
	)}

	lines, err := command.Execute([]int{1, 2}, "")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := []string{"pod/waypoint -n default", "pod/istiod -n istio-system"}
	for i, expected := range want {
		if lines[i] != expected {
			t.Errorf("line %d = %q, want %q", i+1, lines[i], expected)
		}
	}
}

// Each field flag prints exactly one field per line, so a caller always knows
// how many words a line holds.
func TestRefFieldFlagsPrintOneFieldEach(t *testing.T) {
	command := RefCommand{State: refOf([3]string{"web-abc", "diagnostics", "Pod"})}

	for field, want := range map[string]string{
		"name":      "web-abc",
		"namespace": "diagnostics",
		"kind":      "pod",
	} {
		lines, err := command.Execute([]int{1}, field)
		if err != nil {
			t.Fatalf("Execute(%s): %v", field, err)
		}
		if lines[0] != want {
			t.Errorf("--%s = %q, want %q", field, lines[0], want)
		}
	}
}

// Printing an empty line would hand `-n $(kx ref 1 --namespace)` a bare flag
// with no value, and kubectl fails somewhere less obvious than here.
func TestRefNamespaceOfAClusterScopedResourceIsAnError(t *testing.T) {
	command := RefCommand{State: refOf([3]string{"desktop-control-plane", "", "Node"})}

	lines, err := command.Execute([]int{1}, "namespace")
	if err == nil {
		t.Fatalf("Execute = %v, want an error for a resource with no namespace", lines)
	}
	for _, want := range []string{"Node/desktop-control-plane", "namespace"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q\n  missing %q", err, want)
		}
	}
}

// Two field flags would be a third output format nobody asked for, and
// honouring the first silently prints something that looks right.
func TestRefRefusesTwoFieldFlags(t *testing.T) {
	services := switchServices(t, &recordingKubectl{output: namespaceTable})
	if err := services.State.Save(podEntry()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cmd := newRefCommand(services)
	cmd.SetArgs([]string{"1", "--name", "--namespace"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("kx ref --name --namespace succeeded, want a refusal")
	}
	for _, want := range []string{"--name", "--namespace", "pick one"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q\n  missing %q", err, want)
		}
	}
}

// The command is deliberately offline: it answers from saved state, so it is
// instant and works with no cluster reachable. A kubectl call here would make
// the cheapest command in kx pay for a round trip.
func TestRefMakesNoKubectlCall(t *testing.T) {
	kube := &recordingKubectl{output: namespaceTable}
	services := switchServices(t, kube)
	if err := services.State.Save(podEntry()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	before := len(kube.runs) + len(kube.interactive) + len(kube.probes)

	cmd := newRefCommand(services)
	cmd.SetArgs([]string{"1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx ref 1: %v", err)
	}

	if after := len(kube.runs) + len(kube.interactive) + len(kube.probes); after != before {
		t.Errorf("made %d kubectl calls, want none", after-before)
	}
}

// Registered on the root, so the whole index workflow reaches it — and
// registered without the refresh wrapper, which exists to re-run a query after
// kubectl reports a resource gone. This command never asks kubectl anything.
func TestRefIsRegistered(t *testing.T) {
	root := NewRoot(argvServices(t), "test")
	cmd, _, err := root.Find([]string{"ref"})
	if err != nil {
		t.Fatalf("root.Find(ref): %v", err)
	}
	if cmd.Name() != "ref" {
		t.Errorf("root.Find(ref) resolved to %q", cmd.Name())
	}
}

// podEntry is a one-pod listing to resolve index 1 against.
func podEntry() state.State {
	return state.State{
		Resources: state.NewResources([]string{"web-abc"}, kinds.Pod),
		Namespace: "diagnostics",
	}
}

// countingResolver counts resolutions, for the guards that must not pay for
// one on the ordinary path.
type countingResolver struct{ calls int }

func (c *countingResolver) Fields(int) (string, string, kinds.Kind, error) {
	c.calls++
	return "web-abc", "prod", kinds.Pod, nil
}

func (c *countingResolver) Count() (int, error) { return 1, nil }
