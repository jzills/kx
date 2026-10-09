package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/jzills/kx/internal/state"
)

const jsonCollection = `{
    "apiVersion": "v1",
    "items": [
        {"apiVersion": "v1", "kind": "Pod", "metadata": {"name": "nginx-abc-xyz", "namespace": "prod"}},
        {"apiVersion": "v1", "kind": "Pod", "metadata": {"name": "redis-def-uvw", "namespace": "prod"}}
    ],
    "kind": "List",
    "metadata": {"resourceVersion": ""}
}
`

const yamlCollection = `apiVersion: v1
items:
- apiVersion: v1
  kind: Pod
  metadata:
    name: nginx-abc-xyz
    namespace: prod
- apiVersion: v1
  kind: Pod
  metadata:
    name: redis-def-uvw
    namespace: prod
kind: List
metadata:
  resourceVersion: ""
`

// -o json and -o yaml carry metadata.name on every item, so --match can
// narrow them. The refusal said "that output has no rows for it to pick",
// which is wrong for a collection: the rows are there, just not in a table.
func TestMatchNarrowsAJSONCollection(t *testing.T) {
	kube := &recordingKubectl{output: jsonCollection}
	services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})

	stdout, _, err := runCaptured(t, newGetCommand(services),
		[]string{"pods", "-o", "json", "-m", "redis"})
	if err != nil {
		t.Fatalf("kx get pods -o json -m redis: %v", err)
	}

	var doc struct {
		Kind  string `json:"kind"`
		Items []struct {
			Metadata struct{ Name string } `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	// Still a List, so .items[] stays the reader's path.
	if doc.Kind != "List" {
		t.Errorf("kind = %q, want List", doc.Kind)
	}
	if len(doc.Items) != 1 || doc.Items[0].Metadata.Name != "redis-def-uvw" {
		t.Errorf("items = %+v, want redis alone", doc.Items)
	}
}

func TestMatchNarrowsAYAMLCollection(t *testing.T) {
	kube := &recordingKubectl{output: yamlCollection}
	services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})

	stdout, _, err := runCaptured(t, newGetCommand(services),
		[]string{"pods", "-o", "yaml", "-m", "redis"})
	if err != nil {
		t.Fatalf("kx get pods -o yaml -m redis: %v", err)
	}

	var doc struct {
		Kind  string `yaml:"kind"`
		Items []struct {
			Metadata struct{ Name string } `yaml:"metadata"`
		} `yaml:"items"`
	}
	if err := yaml.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not YAML: %v\n%s", err, stdout)
	}
	if doc.Kind != "List" {
		t.Errorf("kind = %q, want List", doc.Kind)
	}
	if len(doc.Items) != 1 || doc.Items[0].Metadata.Name != "redis-def-uvw" {
		t.Errorf("items = %+v, want redis alone", doc.Items)
	}
}

// A term that matches nothing leaves an empty items array rather than
// nothing, so a reader does not have to special-case it.
func TestMatchNarrowingACollectionToNothingKeepsTheDocument(t *testing.T) {
	kube := &recordingKubectl{output: jsonCollection}
	services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})

	stdout, _, err := runCaptured(t, newGetCommand(services),
		[]string{"pods", "-o", "json", "-m", "zzz"})
	if err != nil {
		t.Fatalf("kx get pods -o json -m zzz: %v", err)
	}
	var doc struct {
		Kind  string            `json:"kind"`
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if doc.Kind != "List" || len(doc.Items) != 0 {
		t.Errorf("doc = %+v, want a List with no items", doc)
	}
}

// A projection is still refused: the caller chose the shape, and it may carry
// no names at all. The message should say that rather than claim a document
// has no rows.
func TestMatchStillRefusesAProjection(t *testing.T) {
	for _, args := range [][]string{
		{"pods", "-o", "jsonpath={.items[*].metadata.name}", "-m", "redis"},
		{"pods", "-o", "go-template={{.items}}", "-m", "redis"},
	} {
		t.Run(args[2], func(t *testing.T) {
			kube := &recordingKubectl{output: jsonCollection}
			services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})

			_, _, err := runCaptured(t, newGetCommand(services), args)

			if err == nil {
				t.Fatalf("kx get %s succeeded", strings.Join(args, " "))
			}
			// The specific refusal, not merely something containing
			// "--match": without this, a fallback error further down
			// narrowText satisfies the test and the projection branch could
			// be deleted unnoticed.
			if !strings.Contains(err.Error(), "is a shape you chose") {
				t.Errorf("err = %q, want projectionMatchError's reason", err)
			}
			// The old wording claimed there were no rows, which is what made
			// the json refusal wrong; a projection's problem is different.
			if strings.Contains(err.Error(), "no rows for it to pick") {
				t.Errorf("err = %q, want a reason true of a projection", err)
			}
		})
	}
}
