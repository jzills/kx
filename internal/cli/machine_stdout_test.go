package cli

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// --no-headers says a program is reading stdout, so nothing of kx's goes
// there: an emptied listing names its term on stderr, where kubectl reports
// an empty reply, exactly as -o name already did.
func TestHeaderlessEmptyMatchGoesToStderr(t *testing.T) {
	for _, tc := range []struct{ name, command string }{
		{"get", "get"},
		{"top", "top"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kube := &recordingKubectl{output: "NAME   READY   STATUS   RESTARTS   AGE"}
			services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})

			args := []string{"--no-headers", "-m", "zzz"}
			cmd := newTopCommand(services)
			if tc.command == "get" {
				cmd = newGetCommand(services)
				args = append([]string{"pods"}, args...)
			}
			stdout, stderr, err := runCaptured(t, cmd, args)
			if err != nil {
				t.Fatalf("kx %s %v: %v", tc.command, args, err)
			}
			if strings.TrimSpace(stdout) != "" {
				t.Errorf("stdout = %q, want nothing for the program reading it", stdout)
			}
			if !strings.Contains(stderr, "nothing matches 'zzz'") {
				t.Errorf("stderr = %q, want the term named", stderr)
			}
		})
	}
}

// Another cluster's rows are not indexable, and the caption saying so is a
// line for a person. Ahead of headerless rows it was the first thing a
// program read.
func TestHeaderlessCrossClusterPrintsNoCaption(t *testing.T) {
	for _, tc := range []struct{ name, command string }{
		{"get", "get"},
		{"top", "top"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kube := &recordingKubectl{output: podsOutput}
			services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})

			args := []string{"--context=b", "--no-headers", "-m", "nginx"}
			cmd := newTopCommand(services)
			if tc.command == "get" {
				cmd = newGetCommand(services)
				args = append([]string{"pods"}, args...)
			}
			stdout, _, err := runCaptured(t, cmd, args)
			if err != nil {
				t.Fatalf("kx %s %v: %v", tc.command, args, err)
			}
			if strings.Contains(stdout, "can't be indexed") {
				t.Errorf("stdout = %q, want no caption ahead of headerless rows", stdout)
			}
			if !strings.Contains(stdout, "nginx-abc-xyz") {
				t.Errorf("stdout = %q, want the matching row", stdout)
			}
		})
	}
}

// An empty listing that retired the indexes behind it still owes the way
// back, whoever is reading stdout: an empty reply to --no-headers without a
// term is saved like any other listing, so the note goes to stderr beside the
// notice rather than being dropped with it.
//
// Both tests seed a second entry (seedPrevious), so the cursor sits above the
// bottom of the stack and previousListing has a real listing to name. Seeded
// with one, it returns the zero State at cursor 0 and the note is suppressed
// whatever the guard does — which is what made the first version of the
// control below vacuous.
func TestHeaderlessEmptyListingOffersTheWayBackOnStderr(t *testing.T) {
	for _, tc := range []struct{ name, command string }{
		{"get", "get"},
		{"top", "top"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kube := &recordingKubectl{output: ""}
			services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})
			seedPrevious(t, services)

			args := []string{"--no-headers"}
			cmd := newTopCommand(services)
			if tc.command == "get" {
				cmd = newGetCommand(services)
				args = append([]string{"pods"}, args...)
			}
			stdout, stderr, err := runCaptured(t, cmd, args)
			if err != nil {
				t.Fatalf("kx %s %v: %v", tc.command, args, err)
			}
			if strings.TrimSpace(stdout) != "" {
				t.Errorf("stdout = %q, want nothing for the program reading it", stdout)
			}
			if !strings.Contains(stderr, "none found") {
				t.Errorf("stderr = %q, want the empty listing reported", stderr)
			}
			if !strings.Contains(stderr, "'kx state back' returns to") {
				t.Errorf("stderr = %q, want the way back to the listing this retired", stderr)
			}
		})
	}
}

// A listing kx never saved replaced nothing, so there is no way back to
// offer — the control that keeps the note tied to a saved entry rather than
// to an empty screen. -o name returns from GetCommand.Execute before the
// Save, so dropping the guard would print a note naming a listing that is
// still current.
func TestUnsavedEmptyListingOffersNoWayBack(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		args    []string
		// output is kubectl's reply; cross-cluster listings are never saved,
		// which is the other way an empty one replaces nothing.
		output string
	}{
		{"get -o name", "get", []string{"pods", "-o", "name"}, ""},
		{"top on another cluster", "top",
			[]string{"--context=b", "--no-headers", "-m", "zzz"}, podsOutput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kube := &recordingKubectl{output: tc.output}
			services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})
			seedPrevious(t, services)

			cmd := newTopCommand(services)
			if tc.command == "get" {
				cmd = newGetCommand(services)
			}
			_, stderr, err := runCaptured(t, cmd, tc.args)
			if err != nil {
				t.Fatalf("kx %s %v: %v", tc.command, tc.args, err)
			}
			if strings.Contains(stderr, "'kx state back' returns to") {
				t.Errorf("stderr = %q, want no way back under a listing that replaced nothing", stderr)
			}
		})
	}
}

// seedPrevious pushes a second entry, so the history cursor sits above the
// bottom of the stack. previousListing reads the entry below the cursor and
// returns the zero State at cursor 0, which renders no note at all — so a
// fixture with one entry cannot tell a suppressed note from a guarded one.
func seedPrevious(t *testing.T, services Services) {
	t.Helper()
	// A query of its own: a listing repeating the one the cursor is already
	// on replaces that entry instead of pushing beside it, which leaves the
	// cursor at 0 and the fixture no better than a single entry.
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"web-1", "web-2"}, kinds.Service),
		Namespace: "prod",
		Query:     &state.Query{Resource: "services", Args: []string{}},
	}); err != nil {
		t.Fatalf("seed previous listing: %v", err)
	}
	history, err := services.State.LoadHistory()
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if history.Cursor < 1 {
		t.Fatalf("cursor = %d, want a previous entry for the note to name", history.Cursor)
	}
}

// kx's claim for the collection formats is that stdout carries kubectl's own
// bytes. It carried them plus a trailing blank line: Raw ended the document
// with a newline of its own on top of the one kubectl had already written, so
// `kx get pods -o json` and `kubectl get pods -o json` differed by a line.
// Harmless to a parser, which is why it survived to v0.9.0, and enough to
// fail any checksum or golden-file comparison of the two.
func TestPassthroughWritesKubectlsBytesExactly(t *testing.T) {
	for _, tc := range []struct{ name, reply string }{
		{"-o json", "{\n    \"apiVersion\": \"v1\",\n    \"items\": [],\n    \"kind\": \"List\"\n}\n"},
		{"-o yaml", "apiVersion: v1\nitems: []\nkind: List\n"},
		{"-o name", "pod/web-1\npod/web-2\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			format := strings.TrimPrefix(tc.name, "-o ")
			kube := &recordingKubectl{output: tc.reply}
			services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})
			stdout, _, err := runCaptured(t, newGetCommand(services), []string{"pods", "-o", format})
			if err != nil {
				t.Fatalf("kx get pods -o %s: %v", format, err)
			}
			if stdout != tc.reply {
				t.Errorf("stdout = %q, want kubectl's reply unchanged %q", stdout, tc.reply)
			}
		})
	}
}
