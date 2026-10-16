package cli

import (
	"strings"
	"testing"

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
// back, whoever is reading stdout: --no-headers without a term is a table to
// kx, saved like any other, so the note goes to stderr beside the notice
// rather than being dropped with it.
func TestHeaderlessEmptyListingOffersTheWayBackOnStderr(t *testing.T) {
	for _, tc := range []struct{ name, command string }{
		{"get", "get"},
		{"top", "top"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kube := &recordingKubectl{output: ""}
			services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})

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
// to an empty screen.
func TestUnsavedEmptyListingOffersNoWayBack(t *testing.T) {
	kube := &recordingKubectl{output: ""}
	services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})

	_, stderr, err := runCaptured(t, newGetCommand(services),
		[]string{"pods", "-o", "name"})
	if err != nil {
		t.Fatalf("kx get pods -o name: %v", err)
	}
	if strings.Contains(stderr, "'kx state back' returns to") {
		t.Errorf("stderr = %q, want no way back under a listing that replaced nothing", stderr)
	}
}
