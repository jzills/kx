package cli

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
)

// kx rollout prints kubectl's output as kubectl printed it. It trimmed every
// trailing newline and added one, which the renderer's own newline then
// doubled: a blank line after "rolled back" that kubectl does not print.
// kubectl rollout history does end in a blank line of its own, and keeps it.
func TestRolloutPrintsKubectlsOutputAsItCame(t *testing.T) {
	for _, tc := range []struct {
		action, output string
	}{
		{"undo", "deployment.apps/web rolled back\n"},
		{"restart", "deployment.apps/web restarted\n"},
		{"history", "deployment.apps/web \nREVISION  CHANGE-CAUSE\n1         <none>\n\n"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			services := switchServices(t, &recordingKubectl{output: tc.output})
			saveListing(t, services, kinds.Deployment, "prod", false, "web")
			stdout, _, err := runCaptured(t, newRolloutCommand(services), []string{tc.action, "1"})
			if err != nil {
				t.Fatalf("kx rollout %s 1: %v", tc.action, err)
			}
			if stdout != tc.output {
				t.Errorf("stdout = %q, want kubectl's %q", stdout, tc.output)
			}
		})
	}
}

// Manifests are separated by exactly one "---" and the last is followed by
// nothing. kx yaml kept each manifest's trailing newline and added a
// separator besides, so two blank lines came between every pair and one
// after the last; the separator itself then became YAML's own, since a blank
// line does not end a document and the concatenation parsed as one.
//
// The banners are on stderr, so stdout is the stream and nothing else —
// which is what makes `kx yaml 1 2 > pods.yaml` readable.
func TestYamlSeparatesManifestsByOneDocumentMarker(t *testing.T) {
	const manifest = "apiVersion: v1\nkind: Pod\n"
	services := switchServices(t, &recordingKubectl{output: manifest})
	saveListing(t, services, kinds.Pod, "prod", false, "a", "b")
	stdout, stderr, err := runCaptured(t, newYamlCommand(services), []string{"1", "2"})
	if err != nil {
		t.Fatalf("kx yaml 1 2: %v", err)
	}
	if want := manifest + "---\n" + manifest; stdout != want {
		t.Errorf("stdout = %q\n  want %q", stdout, want)
	}
	for _, name := range []string{"Pod/a · prod", "Pod/b · prod"} {
		if !strings.Contains(stderr, name) {
			t.Errorf("stderr = %q, want %q", stderr, name)
		}
	}
}
