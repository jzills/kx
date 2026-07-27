package cli

import (
	"errors"
	"testing"

	"github.com/jzills/kx/internal/kinds"
)

// kubectl has no exit code that distinguishes a missing resource, so the
// message is all there is to go on.
func TestIsNotFoundRecognizesKubectlMessages(t *testing.T) {
	found := []string{
		`Error from server (NotFound): pods "nginx-abc" not found`,
		`pods "nginx" not found`,
	}
	for _, message := range found {
		if !IsNotFound(errors.New(message)) {
			t.Errorf("IsNotFound(%q) = false, want true", message)
		}
	}

	other := []string{
		`Error from server (Forbidden): pods is forbidden`,
		`Unable to connect to the server: dial tcp: i/o timeout`,
	}
	for _, message := range other {
		if IsNotFound(errors.New(message)) {
			t.Errorf("IsNotFound(%q) = true, want false", message)
		}
	}
	if IsNotFound(nil) {
		t.Error("IsNotFound(nil) = true, want false")
	}
}

func TestIsStaleRecognizesStaleResourceError(t *testing.T) {
	err := StaleResourceError{Kind: kinds.Pod, Name: "nginx"}
	if !isStale(err) {
		t.Error("isStale on StaleResourceError = false, want true")
	}
	if got, want := err.Error(), "Pod/nginx no longer exists"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestIsStaleIgnoresUnrelatedErrors(t *testing.T) {
	if isStale(errors.New("connection refused")) {
		t.Error("isStale on an unrelated error = true, want false")
	}
}

// A probe confirming the resource is gone turns a plain failure into stale
// state; a probe that succeeds leaves the failure alone.
func TestEnsureExists(t *testing.T) {
	gone := &recordingKubectl{probeCode: 1}
	if err := ensureExists(gone, kinds.Pod, "nginx", "prod"); err == nil {
		t.Error("ensureExists on a missing resource returned nil")
	}
	if want := "get Pod nginx -n prod"; joinArgs(gone.probes[0]) != want {
		t.Errorf("probe = %q, want %q", joinArgs(gone.probes[0]), want)
	}

	live := &recordingKubectl{probeCode: 0}
	if err := ensureExists(live, kinds.Pod, "nginx", "prod"); err != nil {
		t.Errorf("ensureExists on a live resource = %v, want nil", err)
	}
}
