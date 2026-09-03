package k8s

import (
	"os"
	"path/filepath"
	"testing"
)

// minimalKubeconfig is a syntactically real kubeconfig — enough for
// clientcmd to parse, never enough to reach a cluster with. current names
// the current-context field; empty means the kubeconfig has none.
func minimalKubeconfig(current, namespace string) string {
	ns := ""
	if namespace != "" {
		ns = "\n    namespace: " + namespace
	}
	return "apiVersion: v1\n" +
		"kind: Config\n" +
		"current-context: " + current + "\n" +
		"contexts:\n" +
		"- name: staging\n" +
		"  context:\n" +
		"    cluster: c\n" +
		"    user: u" + ns + "\n" +
		"clusters:\n" +
		"- name: c\n" +
		"  cluster: {server: https://example.invalid}\n" +
		"users:\n" +
		"- name: u\n" +
		"  user: {}\n"
}

func writeKubeconfig(t *testing.T, current, namespace string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(minimalKubeconfig(current, namespace)), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
}

func TestCurrentContextReadsTheKubeconfig(t *testing.T) {
	writeKubeconfig(t, "staging", "")
	got, err := CurrentContext()
	if err != nil {
		t.Fatalf("CurrentContext: %v", err)
	}
	if got != "staging" {
		t.Errorf("CurrentContext() = %q, want staging", got)
	}
}

// A kubeconfig with no current-context is a legitimate setup — empty means
// "none set", not an error kx should surface as a failure.
func TestCurrentContextEmptyWhenUnset(t *testing.T) {
	writeKubeconfig(t, "", "")
	got, err := CurrentContext()
	if err != nil {
		t.Fatalf("CurrentContext: %v", err)
	}
	if got != "" {
		t.Errorf("CurrentContext() = %q, want empty", got)
	}
}

// No kubeconfig file at all is also a legitimate setup — a fresh machine, or
// in-cluster with no kubeconfig on disk — and must read as "no context" too,
// not as an error.
func TestCurrentContextNoFileAtAll(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "does-not-exist"))
	got, err := CurrentContext()
	if err != nil {
		t.Fatalf("CurrentContext: %v", err)
	}
	if got != "" {
		t.Errorf("CurrentContext() = %q, want empty", got)
	}
}

func TestNamespaceReadsTheCurrentContext(t *testing.T) {
	writeKubeconfig(t, "staging", "prod")
	if got := Namespace(); got != "prod" {
		t.Errorf("Namespace() = %q, want prod", got)
	}
}

// No namespace set on the context falls back to "default", the same way
// kubectl itself treats an unset namespace.
func TestNamespaceFallsBackToDefault(t *testing.T) {
	writeKubeconfig(t, "staging", "")
	if got := Namespace(); got != "default" {
		t.Errorf("Namespace() = %q, want default", got)
	}
}
