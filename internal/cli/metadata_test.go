package cli

import (
	"strings"
	"testing"
)

const labelsJSON = `{"metadata":{"labels":{"tier":"frontend","app":"web"}}}`

func TestMetadataReadReturnsSortedKeys(t *testing.T) {
	kubectl := &recordingKubectl{output: labelsJSON}
	keys, values, err := MetadataReadCommand{
		Kubectl: kubectl, State: pod("nginx"), Field: "labels",
	}.Execute(1)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Sorted, because kubectl returns a JSON object and Go map iteration would
	// reorder the rows on every run.
	if len(keys) != 2 || keys[0] != "app" || keys[1] != "tier" {
		t.Errorf("keys = %v, want [app tier]", keys)
	}
	if values["app"] != "web" {
		t.Errorf("values = %v", values)
	}
	if want := "get Pod nginx -n prod -o json"; joinArgs(kubectl.runs[0]) != want {
		t.Errorf("args = %q, want %q", joinArgs(kubectl.runs[0]), want)
	}
}

func TestMetadataReadHandlesMissingField(t *testing.T) {
	kubectl := &recordingKubectl{output: `{"metadata":{}}`}
	keys, values, err := MetadataReadCommand{
		Kubectl: kubectl, State: pod("nginx"), Field: "annotations",
	}.Execute(1)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(keys) != 0 || len(values) != 0 {
		t.Errorf("keys = %v, values = %v; want empty", keys, values)
	}
}

func TestMetadataWriteSetsAndRemoves(t *testing.T) {
	kubectl := &recordingKubectl{output: `{"metadata":{"labels":{}}}`}
	message, err := MetadataWriteCommand{
		Kubectl: kubectl, State: pod("nginx"), Verb: "label", Field: "labels",
	}.Execute(1, []string{"env"}, map[string]string{"env": "prod"}, []string{"old"}, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// The last run is the write; the first read the current labels.
	write := joinArgs(kubectl.runs[len(kubectl.runs)-1])
	if want := "label Pod nginx -n prod env=prod old-"; write != want {
		t.Errorf("args = %q, want %q", write, want)
	}
	if !strings.Contains(message, "set 1") || !strings.Contains(message, "removed 1") {
		t.Errorf("message = %q, want both counts", message)
	}
	if !strings.HasPrefix(message, "Labeled") {
		t.Errorf("message = %q, want the Labeled verb", message)
	}
}

func TestAnnotateUsesItsOwnVerb(t *testing.T) {
	kubectl := &recordingKubectl{output: `{"metadata":{"annotations":{}}}`}
	message, err := MetadataWriteCommand{
		Kubectl: kubectl, State: pod("nginx"), Verb: "annotate", Field: "annotations",
	}.Execute(1, []string{"note"}, map[string]string{"note": "hi"}, nil, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.HasPrefix(message, "Annotated") {
		t.Errorf("message = %q, want the Annotated verb", message)
	}
}

// Overwriting an existing key needs an explicit opt-in, and the error names
// every conflict rather than just the first.
func TestMetadataWriteRefusesExistingKeys(t *testing.T) {
	kubectl := &recordingKubectl{output: `{"metadata":{"labels":{"env":"dev","app":"web"}}}`}
	_, err := MetadataWriteCommand{
		Kubectl: kubectl, State: pod("nginx"), Verb: "label", Field: "labels",
	}.Execute(1, []string{"env", "app"}, map[string]string{"env": "prod", "app": "api"}, nil, false)
	if err == nil {
		t.Fatal("overwrote existing labels without --overwrite")
	}
	for _, key := range []string{"env", "app"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error = %q, want it to name %q", err, key)
		}
	}
}

func TestMetadataWriteAllowsOverwrite(t *testing.T) {
	kubectl := &recordingKubectl{output: `{"metadata":{"labels":{"env":"dev"}}}`}
	_, err := MetadataWriteCommand{
		Kubectl: kubectl, State: pod("nginx"), Verb: "label", Field: "labels",
	}.Execute(1, []string{"env"}, map[string]string{"env": "prod"}, nil, true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	write := joinArgs(kubectl.runs[len(kubectl.runs)-1])
	if !strings.HasSuffix(write, "--overwrite") {
		t.Errorf("args = %q, want --overwrite forwarded", write)
	}
}

// The error used to be a bare "nothing to set or remove" — no subject, no
// remedy, no closing period, alone among kx's refusals in that respect.
func TestMetadataWriteRejectsEmptyChange(t *testing.T) {
	kubectl := &recordingKubectl{}
	_, err := MetadataWriteCommand{
		Kubectl: kubectl, State: pod("nginx"), Verb: "label", Field: "labels",
	}.Execute(1, nil, nil, nil, false)
	if err == nil {
		t.Fatal("accepted a write with nothing to set or remove")
	}
	if len(kubectl.runs) != 0 {
		t.Error("called kubectl with nothing to do")
	}
	for _, want := range []string{"kx label", "key=value", "--remove"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

func TestParsePairs(t *testing.T) {
	keys, values, err := parsePairs([]string{"env=prod", "tier=web"})
	if err != nil {
		t.Fatalf("parsePairs: %v", err)
	}
	// Order is preserved so the kubectl invocation is predictable.
	if len(keys) != 2 || keys[0] != "env" || keys[1] != "tier" {
		t.Errorf("keys = %v, want [env tier]", keys)
	}
	if values["env"] != "prod" || values["tier"] != "web" {
		t.Errorf("values = %v", values)
	}
}

// A value may legitimately contain '=' (annotations often hold URLs or base64).
func TestParsePairsSplitsOnFirstEquals(t *testing.T) {
	_, values, err := parsePairs([]string{"url=https://example.com/?a=b"})
	if err != nil {
		t.Fatalf("parsePairs: %v", err)
	}
	if values["url"] != "https://example.com/?a=b" {
		t.Errorf("value = %q, was split on the wrong '='", values["url"])
	}
}

func TestParsePairsRejectsMalformed(t *testing.T) {
	for _, arg := range []string{"noequals", "=novalue"} {
		if _, _, err := parsePairs([]string{arg}); err == nil {
			t.Errorf("parsePairs(%q) succeeded, want an error", arg)
		}
	}
}
