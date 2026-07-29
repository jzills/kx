package cli

import (
	"strings"
	"testing"
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
	out, err := YamlCommand{Kubectl: kubectl, State: workload("web", "Deployment")}.Execute(1, nil)
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
		Execute(1, []string{"metadata"})
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
		Execute(1, []string{"containerStatuses"})
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
		Execute(1, []string{"metadata", "spec"})
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
		Execute(1, []string{"nonexistent"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.TrimSpace(out) != "{}" {
		t.Errorf("output = %q, want an empty mapping", out)
	}
}

// Two sibling subtrees at the same depth both holding the requested key. The
// walk is breadth-first, and Go randomises map iteration, so the order siblings
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
			Execute(1, []string{"shared"})
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
		Execute(1, []string{"metadata"})
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
