package kubectl

import (
	"path/filepath"
	"testing"
)

// The version comes from kubectl itself, not from anything kx was built with:
// the shim answers with a version no kx build could have pinned.
func TestDetectClientReportsTheKubectlOnPath(t *testing.T) {
	dir := t.TempDir()
	shim(t, dir, "#!/bin/sh\n"+
		"if [ \"$1 $2 $3 $4\" = \"version --client -o json\" ]; then\n"+
		"  echo '{\"clientVersion\":{\"gitVersion\":\"v1.99.3\"},\"kustomizeVersion\":\"v5\"}'\n"+
		"  exit 0\n"+
		"fi\n"+
		"exit 1\n")

	got := DetectClient()
	if want := filepath.Join(dir, "kubectl"); got.Path != want {
		t.Errorf("Path = %q, want %q", got.Path, want)
	}
	if got.Version != "v1.99.3" {
		t.Errorf("Version = %q, want v1.99.3", got.Version)
	}
}

// A kubectl that is found but cannot answer still reports where it is: that is
// half of what someone running --version wants to know.
func TestDetectClientKeepsThePathWhenTheVersionFails(t *testing.T) {
	dir := t.TempDir()
	shim(t, dir, "#!/bin/sh\nexit 1\n")

	got := DetectClient()
	if want := filepath.Join(dir, "kubectl"); got.Path != want {
		t.Errorf("Path = %q, want %q", got.Path, want)
	}
	if got.Version != "" {
		t.Errorf("Version = %q, want empty for a kubectl that failed", got.Version)
	}
}

func TestDetectClientWithNoKubectlOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if got := DetectClient(); got != (Client{}) {
		t.Errorf("DetectClient() = %+v, want the zero Client", got)
	}
}

func TestClientVersionRejectsAnotherShape(t *testing.T) {
	for _, out := range []string{"", "Client Version: v1.36.1", `{"serverVersion":{"gitVersion":"v1.30.0"}}`} {
		if got := clientVersion([]byte(out)); got != "" {
			t.Errorf("clientVersion(%q) = %q, want empty", out, got)
		}
	}
}
