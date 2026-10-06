package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
)

// splitRender points the renderer at separate stdout and stderr buffers, for
// tests about which stream something reaches.
func splitRender(t *testing.T) (stdout, stderr *bytes.Buffer) {
	t.Helper()
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	render.SetOutput(stdout, stderr, config.DefaultTheme)
	t.Cleanup(func() { render.SetOutput(nil, nil, config.DefaultTheme) })
	return stdout, stderr
}

// A stale index under --json is reported, not refreshed. The fresh listing is
// a table for a person to pick from, and printed to stdout it landed in the
// middle of the document a script was reading: `kx diag 3 --json | jq`
// failed to parse "State was stale — refreshed, pick a new index:". kx diag
// and kx tree read through client-go, so a stale index under them only
// reached the refresh once their not-found counted as stale.
func TestAStaleIndexUnderJSONLeavesStdoutAlone(t *testing.T) {
	for _, command := range []string{"diag", "tree"} {
		t.Run(command, func(t *testing.T) {
			stdout, stderr := splitRender(t)
			kube := &fakeKubectl{output: podsOutput}
			services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})
			// No pods at all, so the indexed api-old is not found.
			services.Kubernetes = func() (kubernetes.Interface, error) {
				return fake.NewSimpleClientset(), nil
			}

			err := Execute(NewRoot(services, "test"), []string{command, "1", "--json"})
			var silent SilentError
			if !errors.As(err, &silent) || silent.Code != 1 {
				t.Fatalf("err = %v, want the stale failure reported and exit 1", err)
			}
			if stdout.Len() > 0 {
				t.Errorf("stdout = %q, want nothing ahead of a reader expecting JSON", stdout.String())
			}
			for _, want := range []string{
				"Pod/api-old no longer exists", "Run 'kx get pods -n prod' to refresh the list.",
			} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr = %q, want %q", stderr.String(), want)
				}
			}
			// Not run again either: a listing nobody sees would replace the
			// one the user's indexes come from.
			if len(kube.calls) > 0 {
				t.Errorf("the listing was run again: %v", kube.calls)
			}
		})
	}
}

// The same holds for a kubectl output format that is not a table, on the
// commands that pass -o through. A table format is still for a person, and
// still refreshed.
func TestAStaleIndexUnderMachineOutputIsNotRefreshed(t *testing.T) {
	const notFound = `Error from server (NotFound): secrets "api-old" not found`
	for _, tc := range []struct {
		args      []string
		refreshed bool
	}{
		{[]string{"secret", "1", "-o", "json"}, false},
		{[]string{"secret", "1", "-ojsonpath={.data}"}, false},
		{[]string{"secret", "1", "--output=name"}, false},
		{[]string{"secret", "1", "-o", "wide"}, true},
		{[]string{"secret", "1"}, true},
		// One value, unwrapped, for $(...) to substitute: the refreshed
		// table landed in the variable a script exported as a credential.
		{[]string{"secret", "1", "--decode", "-k", "token"}, false},
		{[]string{"secret", "1", "--decode", "--key=token"}, false},
		{[]string{"secret", "1", "--decode"}, true},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			stdout, _ := splitRender(t)
			kube := &recordingKubectl{
				errs: []error{kubectl.Error{Stderr: notFound}}, output: podsOutput,
			}
			services := staleServices(t, kube, &state.Query{Resource: "secrets", Args: []string{}})
			if err := services.State.Save(state.State{
				Resources: state.NewResources([]string{"api-old"}, kinds.Secret),
				Namespace: "prod",
				Query:     &state.Query{Resource: "secrets", Args: []string{}},
			}); err != nil {
				t.Fatalf("seed state: %v", err)
			}

			if err := Execute(NewRoot(services, "test"), tc.args); err == nil {
				t.Fatal("a stale index succeeded")
			}
			refreshed := strings.Contains(stdout.String(), "State was stale")
			if refreshed != tc.refreshed {
				t.Errorf("refreshed = %v, want %v; stdout:\n%s", refreshed, tc.refreshed, stdout.String())
			}
		})
	}
}

// A "--" ends the flags kx and kubectl read: what follows is a command for a
// container, and its -o is that command's.
func TestMachineOutputStopsAtTheCommandSeparator(t *testing.T) {
	cmd := newExecCommand(argvServices(t))
	if machineOutput(cmd, []string{"1", "--", "jq", "-o", "json"}) {
		t.Error("a container command's -o read as kx's output format")
	}
	if !machineOutput(cmd, []string{"1", "-o", "json", "--", "sh"}) {
		t.Error("an -o before the separator was not read")
	}
}

// -k is kx's --key only beside --decode, which is kx's alone. kubectl's own -k
// — kustomize, on the commands that pass flags through — still prints a
// table for a person, which a stale index refreshes.
func TestMachineOutputReadsKeyOnlyBesideDecode(t *testing.T) {
	services := argvServices(t)
	if machineOutput(newYamlCommand(services), []string{"1", "-k", "overlays/prod", "-o", "wide"}) {
		t.Error("kubectl's -k read as --decode's key")
	}
	if !machineOutput(newSecretCommand(services, "secret", nil), []string{"1", "--decode", "-k", "token"}) {
		t.Error("--decode -k not read as raw output")
	}
}
