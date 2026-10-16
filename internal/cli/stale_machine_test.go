package cli

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
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
		// Rows with nothing above them are for a program too, whatever the
		// -o says: the refreshed table landed among the rows it was parsing.
		{[]string{"secret", "1", "--no-headers"}, false},
		{[]string{"secret", "1", "--no-headers=false"}, true},
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

// Some commands write a document to stdout without anyone typing -o: kx yaml
// prints a manifest, kx logs a log stream, kx exec whatever ran in the
// container. The refreshed listing landed in the middle of each of those —
// `OUT=$(kx exec 3 -- cat /etc/hostname)` captured a table instead of a
// hostname — so for them stdout is a document whether or not a format was
// asked for, and a stale index reports on stderr and refreshes nothing.
func TestAStaleIndexLeavesADocumentOnStdoutAlone(t *testing.T) {
	const notFound = `Error from server (NotFound): pods "api-old" not found`
	for _, tc := range []struct {
		name string
		args []string
		// kube answers the way a vanished resource makes each command fail:
		// yaml captures kubectl's not-found, while logs and exec stream and
		// learn it from the probe behind their non-zero exit.
		kube *recordingKubectl
	}{
		{"yaml", []string{"yaml", "1"}, &recordingKubectl{
			errs: []error{kubectl.Error{Stderr: notFound}}, output: podsOutput,
		}},
		{"logs", []string{"logs", "1"}, &recordingKubectl{
			output: podsOutput, exitCode: 1, probeCode: 1,
		}},
		{"exec", []string{"exec", "1", "--", "cat", "/etc/hostname"}, &recordingKubectl{
			output: podsOutput, exitCode: 1, probeCode: 1,
		}},
		{"debug", []string{"debug", "1", "--", "ls", "/proc/1/root"}, &recordingKubectl{
			output: podsOutput, exitCode: 1, probeCode: 1,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr := splitRender(t)
			services := staleServices(t, tc.kube, &state.Query{Resource: "pods", Args: []string{}})

			if err := Execute(NewRoot(services, "test"), tc.args); err == nil {
				t.Fatal("a stale index succeeded")
			}
			// Neither the listing nor the instruction that stands in for it
			// when the replay fails: both are for a person to read.
			for _, unwanted := range []string{"State was stale", "to refresh the list"} {
				if strings.Contains(stdout.String(), unwanted) {
					t.Errorf("stdout carries %q, where the document belongs:\n%s",
						unwanted, stdout.String())
				}
			}
			// The instruction is the proof reportStale ran; what names the
			// failure above it is kx's own message or kubectl's, depending on
			// which of the two found the resource gone.
			if want := "Run 'kx get pods -n prod' to refresh the list."; !strings.Contains(
				stderr.String(), want,
			) {
				t.Errorf("stderr = %q, want %q", stderr.String(), want)
			}
			// Nothing run again: a listing nobody sees would replace the one
			// the user's indexes come from.
			if replays := slices.IndexFunc(tc.kube.runs, func(args []string) bool {
				return len(args) > 0 && args[0] == "get" && args[1] == "pods"
			}); replays >= 0 {
				t.Errorf("the listing was replayed: %v", tc.kube.runs)
			}
		})
	}
}

// On a command that still splits its argv by hand, a "--" ends the flags kx
// reads: what follows is the scanner's own trailing argv, and an -o there is
// not an output format kx is asked for.
func TestMachineOutputStopsAtTheCommandSeparator(t *testing.T) {
	cmd := newScanCommand(argvServices(t))
	if machineOutput(cmd, []string{"1", "--", "-o", "json"}) {
		t.Error("an -o past the separator read as kx's output format")
	}
	if !machineOutput(cmd, []string{"1", "-o", "json", "--", "x"}) {
		t.Error("an -o before the separator was not read")
	}
}

// On a command that parses kubectl's flags, what follows "--" is kubectl's,
// unread, so an -o there is an output format like any other.
func TestMachineOutputReadsKubectlsArgvPastTheSeparator(t *testing.T) {
	if !parsedMachineOutput(t, newCopyCommand(argvServices(t)), "1:/etc/hosts", "./hosts", "--", "-o", "json") {
		t.Error("an -o kubectl will get, after --, was not read")
	}
	if parsedMachineOutput(t, newCopyCommand(argvServices(t)), "1:/etc/hosts", "./hosts", "-c", "app") {
		t.Error("a copy with no output format was read as machine output")
	}
}

// -k is kx's --key only beside --decode, which is kx's alone. kubectl's own -k
// — kustomize, on the commands that pass flags through — still prints a
// table for a person, which a stale index refreshes.
func TestMachineOutputReadsKeyOnlyBesideDecode(t *testing.T) {
	services := argvServices(t)
	if parsedMachineOutput(t, newDescribeCommand(services), "1", "-k", "overlays/prod") {
		t.Error("kubectl's -k read as --decode's key")
	}
	if !parsedMachineOutput(t, newSecretCommand(services, "secret", nil), "1", "--decode", "-k", "token") {
		t.Error("--decode -k not read as raw output")
	}
}

// parsedMachineOutput is machineOutput on a command that parses kubectl's
// flags itself, called the way withRefresh calls it: after cobra parsed argv,
// with the positional arguments left over.
func parsedMachineOutput(t *testing.T, cmd *cobra.Command, args ...string) bool {
	t.Helper()
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags(%q): %v", args, err)
	}
	return machineOutput(cmd, cmd.Flags().Args())
}

// On a command that parses kubectl's flags, the format is read from the argv
// kubectl gets — including after "--", which on such a command is kubectl's
// too — and a stale index never puts a listing where a document was expected.
func TestMachineOutputOnAParsingCommand(t *testing.T) {
	services := argvServices(t)
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"1"}, false},
		{[]string{"1", "-o", "wide"}, false},
		{[]string{"1", "-o", "json"}, true},
		{[]string{"1", "-ojsonpath={.data}"}, true},
		{[]string{"1", "--", "-o", "yaml"}, true},
		{[]string{"1", "--decode"}, false},
		{[]string{"1", "--decode", "--key="}, true},
	} {
		if got := parsedMachineOutput(t, newSecretCommand(services, "secret", nil), tc.args...); got != tc.want {
			t.Errorf("kx secret %s: machineOutput = %v, want %v", joinArgs(tc.args), got, tc.want)
		}
	}
}
