package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

const crossClusterPods = "NAME   READY   STATUS\n" +
	"api    1/1     Running\n" +
	"web    1/1     Running\n"

// crossClusterServices is a current listing of one pod, nginx, in prod — the
// listing a cross-cluster one must leave resolving.
func crossClusterServices(t *testing.T, kube *recordingKubectl) Services {
	t.Helper()
	services := switchServices(t, kube)
	saveListing(t, services, kinds.Pod, "prod", false, "nginx")
	return services
}

// assertListingUntouched checks the stack still holds the entries it started
// with, and index 1 still names nginx in prod.
func assertListingUntouched(t *testing.T, services Services, entries int) {
	t.Helper()
	history, err := services.State.LoadHistory()
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(history.States) != entries {
		t.Errorf("history holds %d entries, want the %d it started with", len(history.States), entries)
	}
	name, namespace, _, err := services.State.Resolve(state.Ref{Index: 1})
	if err != nil || name != "nginx" || namespace != "prod" {
		t.Errorf("index 1 = %s in %s (err %v), want nginx in prod", name, namespace, err)
	}
}

// A listing taken in another cluster was numbered and saved as though it came
// from this one — its rows labelled with the current context and namespace —
// so `kx get pods --context=b` then `kx delete 1` deleted a same-named pod
// here. It is printed as kubectl gave it instead, unnumbered and unsaved, the
// way kx already prints output it cannot index, and the listing before it
// keeps its numbers.
func TestGetFromAnotherClusterIsPrintedNotSaved(t *testing.T) {
	for _, flag := range [][]string{{"--context=b"}, {"--kubeconfig", "/tmp/b"}, {"-s", "https://b:6443"}} {
		kube := &recordingKubectl{output: crossClusterPods}
		services := crossClusterServices(t, kube)
		args := append([]string{"pods"}, flag...)
		stdout, _, err := runCaptured(t, newGetCommand(services), args)
		if err != nil {
			t.Fatalf("kx get %v: %v", args, err)
		}
		if len(kube.runs) != 1 || joinArgs(kube.runs[0]) != "get pods "+joinArgs(flag) {
			t.Errorf("kubectl = %q, want the flag forwarded", kube.runs)
		}
		if !strings.Contains(stdout, "api    1/1     Running") ||
			!strings.Contains(stdout, "listings with '"+strings.SplitN(flag[0], "=", 2)[0]+"' can't be indexed") {
			t.Errorf("kx get %v printed %q, want kubectl's rows and the caption", args, stdout)
		}
		if strings.Contains(stdout, " X ") {
			t.Errorf("kx get %v printed %q, want no index column", args, stdout)
		}
		assertListingUntouched(t, services, 1)
	}
}

// --match still narrows the rows, which kx otherwise applies as it numbers
// them.
func TestGetFromAnotherClusterStillMatches(t *testing.T) {
	kube := &recordingKubectl{output: crossClusterPods}
	stdout, _, err := runCaptured(t, newGetCommand(crossClusterServices(t, kube)),
		[]string{"pods", "--context=b", "-m", "api"})
	if err != nil {
		t.Fatalf("kx get pods --context=b -m api: %v", err)
	}
	if !strings.Contains(stdout, "api") || strings.Contains(stdout, "web") {
		t.Errorf("stdout = %q, want api alone", stdout)
	}
}

// Nothing found in the other cluster is captioned without the current
// namespace, which it is not about, and offers no `kx state back`: nothing was
// saved, so there is nowhere back to go.
//
// A Deployment listing sits under the current one, so there is a way back for
// the note to offer if anything were saved.
func TestGetFromAnotherClusterFindingNothing(t *testing.T) {
	kube := &recordingKubectl{output: ""}
	services := switchServices(t, kube)
	saveListing(t, services, kinds.Deployment, "prod", false, "web")
	saveListing(t, services, kinds.Pod, "prod", false, "nginx")
	stdout, _, err := runCaptured(t, newGetCommand(services), []string{"pods", "--context=b"})
	if err != nil {
		t.Fatalf("kx get pods --context=b: %v", err)
	}
	if !strings.Contains(stdout, "none found") || strings.Contains(stdout, "prod") ||
		strings.Contains(stdout, "state back") {
		t.Errorf("stdout = %q, want 'none found' with no namespace and no state back", stdout)
	}
	assertListingUntouched(t, services, 2)
}

// kx top reads the current cluster twice beside the listing itself — the
// metrics-server probe and the pod limits its CPU%/MEM% are computed against —
// so with another cluster named it does neither, and prints kubectl top's own
// table, unnumbered and unsaved.
func TestTopFromAnotherClusterIsPrintedNotSaved(t *testing.T) {
	for _, args := range [][]string{{"--context=b"}, {"nodes", "--context=b"}} {
		kube := &recordingKubectl{output: "NAME   CPU(cores)   MEMORY(bytes)\napi    5m           20Mi\n"}
		services := crossClusterServices(t, kube)
		stdout, _, err := runCaptured(t, newTopCommand(services), args)
		if err != nil {
			t.Fatalf("kx top %v: %v", args, err)
		}
		if len(kube.probes) != 0 {
			t.Errorf("kx top %v probed %q, want no metrics probe of the current cluster", args, kube.probes)
		}
		if len(kube.runs) != 1 || kube.runs[0][0] != "top" {
			t.Errorf("kx top %v ran %q, want kubectl top alone", args, kube.runs)
		}
		if !strings.Contains(stdout, "api    5m") || strings.Contains(stdout, "CPU%") ||
			!strings.Contains(stdout, "can't be indexed") {
			t.Errorf("kx top %v printed %q, want kubectl's table and the caption", args, stdout)
		}
		assertListingUntouched(t, services, 1)
	}
}

// --json and the HTML page are built from numbered rows, and would report
// none. The page is asked for with --out, which writes a file, rather than
// --html, which would serve one until stopped if the refusal were missing.
func TestTopFromAnotherClusterRefusesJSONAndHTML(t *testing.T) {
	for _, flag := range [][]string{{"--json"}, {"--out", filepath.Join(t.TempDir(), "top.html")}} {
		kube := &recordingKubectl{}
		_, _, err := runCaptured(t, newTopCommand(crossClusterServices(t, kube)),
			append([]string{"--context=b"}, flag...))
		if err == nil || !strings.Contains(err.Error(), "'"+flag[0]+"' cannot be combined with '--context'") {
			t.Errorf("kx top --context=b %s: err = %v, want the refusal", flag[0], err)
		}
		if len(kube.runs)+len(kube.probes) != 0 {
			t.Errorf("kx top --context=b %s called kubectl: %q %q", flag, kube.runs, kube.probes)
		}
	}
}

// The caption goes to stdout, so ahead of output that isn't a table it is a
// line of prose in the middle of JSON, YAML or names: `kx get ns --context=b
// -o json | jq` failed to parse. Output kx can't number is printed exactly as
// it came, as it is without a cluster flag. A table keeps its caption, which
// proves the caption can still be seen at all.
func TestGetFromAnotherClusterLeavesMachineOutputAlone(t *testing.T) {
	for _, tc := range []struct {
		output  string
		args    []string
		caption bool
	}{
		{`{"items": []}`, []string{"-o", "json"}, false},
		{"items: []", []string{"-o=yaml"}, false},
		{"pod/api\npod/web", []string{"-o", "name"}, false},
		{"api web", []string{"-o", "jsonpath={.items[*].metadata.name}"}, false},
		{"POD\napi\nweb", []string{"-o", "custom-columns=POD:.metadata.name"}, true},
		{crossClusterPods, []string{"-owide"}, true},
	} {
		kube := &recordingKubectl{output: tc.output}
		services := crossClusterServices(t, kube)
		args := append([]string{"pods", "--context=b"}, tc.args...)
		stdout, _, err := runCaptured(t, newGetCommand(services), args)
		if err != nil {
			t.Fatalf("kx get %v: %v", args, err)
		}
		captioned := strings.Contains(stdout, "can't be indexed")
		if captioned != tc.caption {
			t.Errorf("kx get %v captioned=%v, want %v: %q", args, captioned, tc.caption, stdout)
		}
		if !tc.caption && strings.TrimSpace(stdout) != strings.TrimSpace(tc.output) {
			t.Errorf("kx get %v printed %q, want kubectl's output exactly", args, stdout)
		}
		assertListingUntouched(t, services, 1)
	}
}

// A watch kx can't draw as a live table streams straight through, and had the
// same caption ahead of it: `kx get ns -w -o name` opened with a line that is
// not a name. A custom-columns stream is still a table and keeps it.
func TestWatchStreamLeavesMachineOutputAlone(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		caption bool
	}{
		{[]string{"-o", "name"}, false},
		{[]string{"--output=json"}, false},
		{[]string{"-oyaml"}, false},
		{[]string{"-o", "custom-columns=NS:.metadata.name"}, true},
	} {
		kube := &recordingKubectl{}
		args := append([]string{"ns", "-w"}, tc.args...)
		stdout, _, err := runCaptured(t, newGetCommand(switchServices(t, kube)), args)
		if err != nil {
			t.Fatalf("kx get %v: %v", args, err)
		}
		if len(kube.interactive) != 1 {
			t.Fatalf("kx get %v streamed %q, want one kubectl watch", args, kube.interactive)
		}
		captioned := strings.Contains(stdout, "can't be indexed")
		if captioned != tc.caption {
			t.Errorf("kx get %v captioned=%v, want %v: %q", args, captioned, tc.caption, stdout)
		}
	}
}
