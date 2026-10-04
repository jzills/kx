package cli

import (
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

// Manifests are separated by one blank line and the last is followed by
// none. kx yaml kept each manifest's trailing newline and added a blank line
// between them besides, so two came between every pair and one after the
// last.
func TestYamlSeparatesManifestsByOneBlankLine(t *testing.T) {
	const manifest = "apiVersion: v1\nkind: Pod\n"
	services := switchServices(t, &recordingKubectl{output: manifest})
	saveListing(t, services, kinds.Pod, "prod", false, "a", "b")
	stdout, _, err := runCaptured(t, newYamlCommand(services), []string{"1", "2"})
	if err != nil {
		t.Fatalf("kx yaml 1 2: %v", err)
	}
	want := "Pod/a · prod\n" + manifest + "\nPod/b · prod\n" + manifest
	if stdout != want {
		t.Errorf("stdout = %q\n  want %q", stdout, want)
	}
}
