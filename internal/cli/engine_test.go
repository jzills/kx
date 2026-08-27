package cli

import (
	"strconv"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/scanner"
)

func TestResolveEngineByIndex(t *testing.T) {
	name, err := resolveEngine("1")
	if err != nil {
		t.Fatalf("resolveEngine: %v", err)
	}
	if name != "scout" {
		t.Errorf("resolveEngine(\"1\") = %q, want scout", name)
	}
}

func TestResolveEngineByName(t *testing.T) {
	name, err := resolveEngine("trivy")
	if err != nil {
		t.Fatalf("resolveEngine: %v", err)
	}
	if name != "trivy" {
		t.Errorf("resolveEngine = %q, want trivy", name)
	}
}

func TestResolveEngineOutOfRange(t *testing.T) {
	// The upper bound is derived rather than written out: this test used to
	// hard-code the first index past the end, so registering a third engine
	// turned an out-of-range case into a valid one and failed here instead of
	// wherever the real problem would have been.
	past := strconv.Itoa(len(scanner.Names()) + 1)
	for _, argument := range []string{"0", past} {
		_, err := resolveEngine(argument)
		if err == nil {
			t.Errorf("resolveEngine(%q) succeeded, want an error", argument)
		}
		if err != nil && !strings.Contains(err.Error(), "out of range") {
			t.Errorf("resolveEngine(%q) error = %q, want an out-of-range message", argument, err)
		}
	}
}

func TestResolveEngineUnknownName(t *testing.T) {
	_, err := resolveEngine("nonexistent")
	if err == nil || !strings.Contains(err.Error(), "Unknown engine") {
		t.Errorf("error = %v, want an unknown-engine message", err)
	}
}

func TestResolveEngineByIndexReachesTheLastEngine(t *testing.T) {
	names := scanner.Names()
	last := strconv.Itoa(len(names))
	name, err := resolveEngine(last)
	if err != nil {
		t.Fatalf("resolveEngine(%q): %v", last, err)
	}
	if want := names[len(names)-1]; name != want {
		t.Errorf("resolveEngine(%q) = %q, want %q", last, name, want)
	}
}
