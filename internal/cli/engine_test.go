package cli

import (
	"strings"
	"testing"
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
	for _, argument := range []string{"0", "3"} {
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
