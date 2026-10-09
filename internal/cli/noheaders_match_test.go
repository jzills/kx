package cli

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/state"
)

// --match and --no-headers compose: kx asks kubectl for the header, finds the
// NAME column in it, narrows, and drops the header on the way out.
//
// It used to refuse the pair — "kx finds each row's name under kubectl's NAME
// header" — which is true of the reply but not a reason, since kx is the one
// asking. --no-headers describes the output the caller wants, not the reply
// kx has to read to produce it.
func TestMatchAndNoHeadersCompose(t *testing.T) {
	kube := &recordingKubectl{output: podsOutput}
	services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})

	stdout, _, err := runCaptured(t, newGetCommand(services),
		[]string{"pods", "--no-headers", "-m", "nginx"})
	if err != nil {
		t.Fatalf("kx get pods --no-headers -m nginx: %v", err)
	}

	// The header is gone, and so is the row the term excluded.
	if strings.Contains(stdout, "NAME") || strings.Contains(stdout, "STATUS") {
		t.Errorf("stdout = %q, want no header row", stdout)
	}
	if !strings.Contains(stdout, "nginx-abc-xyz") {
		t.Errorf("stdout = %q, want the matching row", stdout)
	}
	if strings.Contains(stdout, "redis-def-uvw") {
		t.Errorf("stdout = %q, want the non-matching row dropped", stdout)
	}
	// The strip is the whole mechanism: kubectl must be asked *with* the
	// header, or there is no NAME column to narrow by.
	if len(kube.runs) == 0 {
		t.Fatal("kubectl was not called")
	}
	if got := joinArgs(kube.runs[0]); strings.Contains(got, "--no-headers") {
		t.Errorf("kubectl args = %q, want --no-headers withheld so the header comes back", got)
	}
}

// Without a term, --no-headers is forwarded as it always was: kx has no
// reason to read the reply, so there is nothing to withhold.
func TestNoHeadersAloneIsStillForwarded(t *testing.T) {
	kube := &recordingKubectl{output: podsOutput}
	services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})

	if _, _, err := runCaptured(t, newGetCommand(services),
		[]string{"pods", "--no-headers"}); err != nil {
		t.Fatalf("kx get pods --no-headers: %v", err)
	}
	if len(kube.runs) == 0 {
		t.Fatal("kubectl was not called")
	}
	if got := joinArgs(kube.runs[0]); !strings.Contains(got, "--no-headers") {
		t.Errorf("kubectl args = %q, want --no-headers passed through", got)
	}
}

// A term that matches nothing under --no-headers prints no row, and names the
// term as every other emptied listing does.
//
// The caption lands on stdout, which is where kx already puts it for
// `kx get pods --no-headers` in an empty namespace — pre-existing and not
// changed here, though arguably it belongs on stderr for output a program
// reads, as -o name's does.
func TestNoHeadersWithAMatchThatFindsNothingPrintsNoRow(t *testing.T) {
	kube := &recordingKubectl{output: podsOutput}
	services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})

	stdout, stderr, err := runCaptured(t, newGetCommand(services),
		[]string{"pods", "--no-headers", "-m", "zzz"})
	if err != nil {
		t.Fatalf("kx get pods --no-headers -m zzz: %v", err)
	}
	for _, name := range []string{"nginx-abc-xyz", "redis-def-uvw"} {
		if strings.Contains(stdout, name) {
			t.Errorf("stdout = %q, want no row — the term matched none", stdout)
		}
	}
	if !strings.Contains(stdout+stderr, "nothing matches 'zzz'") {
		t.Errorf("output = %q / %q, want the term named", stdout, stderr)
	}
}
