package cli

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/state"
)

// limitScanServices is a sweep of two images: a:v1, clean, and z:v1, with
// one critical vulnerability.
func limitScanServices(t *testing.T) Services {
	t.Helper()
	return Services{
		Kubectl: &fakeKubectl{
			namespace: "prod",
			output: `{"items":[{"kind":"Pod","spec":{"containers":[{"image":"z:v1"},` +
				`{"image":"a:v1"}]}}]}`,
		},
		State:  &state.Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")},
		Config: config.Default(),
		Scanner: &fakeScanner{captures: []captured{
			{image: "z:v1", stdout: trivyCritical},
			{image: "a:v1", stdout: `{"Results":[]}`},
		}},
	}
}

// A limit keeps the summary's first images once --sort has ordered it, and
// says how many there were; the gate still reads every image scanned, so the
// critical one the limit cut fails it. The control is the same sweep sorted
// the other way, where the critical image is the one kept.
func TestScanLimitKeepsTheFirstImagesAndGatesOnEvery(t *testing.T) {
	cmd := newScanCommand(limitScanServices(t))
	out := captureRender(t)
	err := cmd.RunE(cmd, []string{
		"--engine", "trivy", "-n", "prod", "--sort", "image", "--limit", "1", "--fail-on", "critical",
	})
	var silent SilentError
	if !errors.As(err, &silent) || silent.Code != findingsExitCode {
		t.Errorf("RunE = %v, want --fail-on to exit %d on the image the limit cut", err, findingsExitCode)
	}
	if !strings.Contains(out.String(), "a:v1") || strings.Contains(out.String(), "z:v1") {
		t.Errorf("want a:v1 alone in the summary:\n%s", out)
	}
	if !strings.Contains(out.String(), "1 of 2 images shown") {
		t.Errorf("output missing '1 of 2 images shown':\n%s", out)
	}

	cmd = newScanCommand(limitScanServices(t))
	out = captureRender(t)
	if err := cmd.RunE(cmd, []string{"--engine", "trivy", "-n", "prod", "--sort", "-crit", "--limit", "1"}); err != nil {
		t.Fatalf("control: %v", err)
	}
	if !strings.Contains(out.String(), "z:v1") || strings.Contains(out.String(), "a:v1") {
		t.Errorf("control: want z:v1 alone in the summary:\n%s", out)
	}
}

// --json carries the images kept, as the table does.
func TestScanLimitCutsTheDocument(t *testing.T) {
	cmd := newScanCommand(limitScanServices(t))
	out := captureRender(t)
	if err := cmd.RunE(cmd, []string{"--engine", "trivy", "-n", "prod", "--sort", "image", "--limit", "1", "--json"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(out.String(), `"a:v1"`) || strings.Contains(out.String(), `"z:v1"`) {
		t.Errorf("want a:v1 alone in the document:\n%s", out)
	}
}

// Each refusal is made before kubectl or any scanner runs.
func TestScanLimitRefusals(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--limit", "0"}, "Invalid value for '--limit': '0' keeps no rows — give a count of 1 or more."},
		{[]string{"--limit", "five"}, "Invalid value for '--limit': 'five' is not a valid int."},
		{[]string{"--limit"}, "'--limit' needs a value."},
		{[]string{"--limit", "2", "--full"},
			"'--limit' cannot be combined with '--full' — '--full' streams the scanner's own report, which kx does not parse, so there is no table to cut."},
	} {
		kube := &fakeKubectl{namespace: "prod", output: `{"items":[]}`}
		fake := &fakeScanner{}
		cmd := newScanCommand(Services{
			Kubectl: kube, Config: config.Default(), Scanner: fake,
			State: &state.Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")},
		})
		err := cmd.RunE(cmd, tc.args)
		if err == nil || err.Error() != tc.want {
			t.Errorf("kx scan %s: err = %v, want %q", strings.Join(tc.args, " "), err, tc.want)
		}
		if len(kube.calls) > 0 || len(fake.argv) > 0 {
			t.Errorf("kx scan %s: ran kubectl %q or the scanner %q", strings.Join(tc.args, " "), kube.calls, fake.argv)
		}
	}
}
