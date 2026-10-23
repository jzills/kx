package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
)

const manifest = `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  labels:
    app: web
spec:
  replicas: 2
  template:
    metadata:
      name: web-pod
      labels:
        app: web
    spec:
      containers:
        - name: app
          image: nginx
status:
  containerStatuses:
    - ready: true
`

func TestYamlReturnsRawManifestByDefault(t *testing.T) {
	kubectl := &recordingKubectl{output: manifest}
	out, err := YamlCommand{Kubectl: kubectl, State: workload("web", "Deployment")}.Execute(state.Ref{Index: 1}, nil, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != manifest {
		t.Error("manifest was modified when no fields were requested")
	}
	if want := "get Deployment web -n prod -o yaml"; joinArgs(kubectl.runs[0]) != want {
		t.Errorf("args = %q, want %q", joinArgs(kubectl.runs[0]), want)
	}
}

// Shallowest-wins: a workload's own metadata, not its pod template's.
func TestYamlShowPrefersShallowestKey(t *testing.T) {
	kubectl := &recordingKubectl{output: manifest}
	out, err := YamlCommand{Kubectl: kubectl, State: workload("web", "Deployment")}.
		Execute(state.Ref{Index: 1}, []string{"metadata"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "name: web") {
		t.Errorf("output = %q, want the top-level metadata", out)
	}
	if strings.Contains(out, "web-pod") {
		t.Errorf("output = %q, want the template's metadata excluded", out)
	}
}

// A key that only exists deep in the manifest is still found.
func TestYamlShowFindsNestedOnlyKey(t *testing.T) {
	kubectl := &recordingKubectl{output: manifest}
	out, err := YamlCommand{Kubectl: kubectl, State: workload("web", "Deployment")}.
		Execute(state.Ref{Index: 1}, []string{"containerStatuses"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "ready: true") {
		t.Errorf("output = %q, want the nested key", out)
	}
}

func TestYamlShowMultipleKeys(t *testing.T) {
	kubectl := &recordingKubectl{output: manifest}
	out, err := YamlCommand{Kubectl: kubectl, State: workload("web", "Deployment")}.
		Execute(state.Ref{Index: 1}, []string{"metadata", "spec"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"metadata:", "spec:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want %q", out, want)
		}
	}
}

func TestYamlShowUnknownKeyYieldsEmpty(t *testing.T) {
	kubectl := &recordingKubectl{output: manifest}
	out, err := YamlCommand{Kubectl: kubectl, State: workload("web", "Deployment")}.
		Execute(state.Ref{Index: 1}, []string{"nonexistent"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.TrimSpace(out) != "{}" {
		t.Errorf("output = %q, want an empty mapping", out)
	}
}

// Two sibling subtrees at the same depth both holding the requested key. The
// walk is breadth-first, and Go randomizes map iteration, so the order siblings
// enter the frontier varies run to run — an unordered walk answers the same
// manifest differently on different runs.
const ambiguousManifest = `
apiVersion: v1
kind: Pod
alpha:
  shared: from-alpha
beta:
  shared: from-beta
`

func TestYamlShowResolvesTiesTheSameWayEveryRun(t *testing.T) {
	var seen []string
	for i := 0; i < 40; i++ {
		kubectl := &recordingKubectl{output: ambiguousManifest}
		out, err := YamlCommand{Kubectl: kubectl, State: pod("web")}.
			Execute(state.Ref{Index: 1}, []string{"shared"}, nil)
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		seen = append(seen, strings.TrimSpace(out))
	}
	for _, got := range seen {
		if got != seen[0] {
			t.Fatalf("same manifest resolved two ways: %q and %q", seen[0], got)
		}
	}
}

// PyYAML indents two spaces; yaml.v3 defaults to four, which would make
// `kx yaml --show` disagree with the manifest kubectl printed.
func TestYamlShowUsesTwoSpaceIndent(t *testing.T) {
	kubectl := &recordingKubectl{output: manifest}
	out, err := YamlCommand{Kubectl: kubectl, State: workload("web", "Deployment")}.
		Execute(state.Ref{Index: 1}, []string{"metadata"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if trimmed == "" {
			continue
		}
		if indent := len(line) - len(trimmed); indent%2 != 0 || indent > 4 {
			t.Errorf("line %q has %d-space indent, want multiples of two", line, indent)
		}
	}
	if !strings.Contains(out, "\n  labels:") {
		t.Errorf("output = %q, want two-space nesting", out)
	}
}

// yamlServices seeds two pods, so kx yaml has more than one manifest to join.
func yamlServices(t *testing.T, kube kubectl.Service) Services {
	t.Helper()
	store := &state.Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")}
	if err := store.Save(state.State{
		Resources: state.NewResources([]string{"web-1", "web-2"}, kinds.Pod),
		Namespace: "prod",
		Query:     &state.Query{Resource: "pods", Args: []string{}},
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	return Services{Kubectl: kube, State: store, Index: index.Service{}, Config: config.Default()}
}

func podJSON(name string) string {
	return `{
    "apiVersion": "v1",
    "kind": "Pod",
    "metadata": {
        "name": "` + name + `"
    }
}`
}

// `---` is YAML's document separator and means nothing in JSON, so writing it
// between two objects left output no reader accepts: `kx yaml 1 2 -o json`
// failed in jq and in every JSON parser. Several names are one List, which is
// what kubectl itself answers `kubectl get pod a b -o json` with.
func TestYamlMergesJSONIntoOneList(t *testing.T) {
	kube := &recordingKubectl{outputs: []string{podJSON("web-1"), podJSON("web-2")}}
	stdout, _, err := runCaptured(t, newYamlCommand(yamlServices(t, kube)), []string{"1", "2", "-o", "json"})
	if err != nil {
		t.Fatalf("kx yaml 1 2 -o json: %v", err)
	}
	var doc struct {
		Kind  string `json:"kind"`
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if doc.Kind != "List" {
		t.Errorf("kind = %q, want List", doc.Kind)
	}
	if len(doc.Items) != 2 {
		t.Fatalf("items = %d, want both manifests", len(doc.Items))
	}
	for i, want := range []string{"web-1", "web-2"} {
		if got := doc.Items[i].Metadata.Name; got != want {
			t.Errorf("items[%d] = %q, want %q", i, got, want)
		}
	}
}

// One name is one object, as kubectl answers one name. Wrapping it in a List
// because the merge exists would change what every single-index call prints.
func TestYamlSingleIndexJSONStaysABareObject(t *testing.T) {
	kube := &recordingKubectl{output: podJSON("web-1")}
	stdout, _, err := runCaptured(t, newYamlCommand(yamlServices(t, kube)), []string{"1", "-o", "json"})
	if err != nil {
		t.Fatalf("kx yaml 1 -o json: %v", err)
	}
	var doc struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if doc.Kind != "Pod" {
		t.Errorf("kind = %q, want the bare Pod kubectl answers one name with", doc.Kind)
	}
}

// The separator belongs to YAML alone. Written whatever the format, it put a
// bare `---` line into -o name output a script reads line by line, and into
// whatever a Go template or jsonpath produced.
func TestYamlSeparatesDocumentsOnlyInYAML(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      []string
		reply     string
		separated bool
	}{
		{"default", []string{"1", "2"}, manifest, true},
		{"-o yaml", []string{"1", "2", "-o", "yaml"}, manifest, true},
		{"-o json", []string{"1", "2", "-o", "json"}, podJSON("web-1"), false},
		{"-o name", []string{"1", "2", "-o", "name"}, "pod/web-1", false},
		{"jsonpath", []string{"1", "2", "-o", "jsonpath={.metadata.name}"}, "web-1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kube := &recordingKubectl{output: tc.reply}
			stdout, _, err := runCaptured(t, newYamlCommand(yamlServices(t, kube)), tc.args)
			if err != nil {
				t.Fatalf("kx yaml %v: %v", tc.args, err)
			}
			separated := false
			for _, line := range strings.Split(stdout, "\n") {
				if strings.TrimSpace(line) == "---" {
					separated = true
				}
			}
			if separated != tc.separated {
				t.Errorf("separated by --- = %v, want %v\n%s", separated, tc.separated, stdout)
			}
		})
	}
}

// Each banner introduces the manifest printed under it. The separator goes to
// stdout and the banner to stderr, so on a terminal they interleave in call
// order: written beside the manifest rather than before the banner, that
// order read `banner / --- / manifest`, leaving every banner but the first
// divorced from the manifest it names.
//
// Checked on one buffer, because the defect is only visible where both
// streams land together — which is a terminal, and was how it was reported.
func TestYamlBannerIntroducesTheManifestUnderIt(t *testing.T) {
	kube := &recordingKubectl{outputs: []string{"kind: Pod\nname: web-1", "kind: Pod\nname: web-2"}}
	var both bytes.Buffer
	render.SetOutput(&both, &both, "github-dark")
	cmd := newYamlCommand(yamlServices(t, kube))
	cmd.SetArgs([]string{"1", "2"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx yaml 1 2: %v", err)
	}

	var shape []string
	for _, line := range strings.Split(both.String(), "\n") {
		switch {
		case strings.Contains(line, "web-1"), strings.Contains(line, "web-2"):
			if strings.Contains(line, "·") {
				shape = append(shape, "banner")
				continue
			}
			shape = append(shape, "manifest")
		case strings.TrimSpace(line) == "---":
			shape = append(shape, "---")
		}
	}
	want := []string{"banner", "manifest", "---", "banner", "manifest"}
	if strings.Join(shape, " ") != strings.Join(want, " ") {
		t.Errorf("order = %v, want %v\n%s", shape, want, both.String())
	}
}
