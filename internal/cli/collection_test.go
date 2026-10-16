package cli

import (
	"strings"
	"testing"
)

// A narrowed document keeps kubectl's bytes. json.Marshal escaped &, < and >
// for HTML, so an annotation holding a URL came back with & in it.
func TestNarrowJSONCollectionKeepsCharactersHTMLWouldEscape(t *testing.T) {
	reply := `{
    "apiVersion": "v1",
    "items": [
        {
            "metadata": {
                "annotations": {
                    "link": "https://example.com/?a=1&b=<2>"
                },
                "name": "web-1"
            }
        },
        {
            "metadata": {
                "name": "db-1"
            }
        }
    ],
    "kind": "List"
}
`
	got, err := narrowCollection(reply, "web", false)
	if err != nil {
		t.Fatalf("narrowCollection: %v", err)
	}
	if !strings.Contains(got, `"https://example.com/?a=1&b=<2>"`) {
		t.Errorf("narrowed document changed the annotation:\n%s", got)
	}
	if strings.Contains(got, "db-1") {
		t.Errorf("narrowed document kept db-1:\n%s", got)
	}
}

// kubectl prints a sequence's dashes flush with its key. The encoder's
// default indented them, so every narrowed document diffed against kubectl's
// on every line of every item.
func TestNarrowYAMLCollectionKeepsKubectlsSequenceIndent(t *testing.T) {
	reply := "apiVersion: v1\n" +
		"items:\n" +
		"- apiVersion: v1\n" +
		"  kind: Pod\n" +
		"  metadata:\n" +
		"    name: web-1\n" +
		"  spec:\n" +
		"    containers:\n" +
		"    - image: nginx\n" +
		"      name: web\n" +
		"- apiVersion: v1\n" +
		"  kind: Pod\n" +
		"  metadata:\n" +
		"    name: db-1\n" +
		"kind: List\n" +
		"metadata:\n" +
		"  resourceVersion: \"\"\n"
	want := "apiVersion: v1\n" +
		"items:\n" +
		"- apiVersion: v1\n" +
		"  kind: Pod\n" +
		"  metadata:\n" +
		"    name: web-1\n" +
		"  spec:\n" +
		"    containers:\n" +
		"    - image: nginx\n" +
		"      name: web\n" +
		"kind: List\n" +
		"metadata:\n" +
		"  resourceVersion: \"\"\n"

	got, err := narrowCollection(reply, "web", true)
	if err != nil {
		t.Fatalf("narrowCollection: %v", err)
	}
	if got != want {
		t.Errorf("narrowed document =\n%s\nwant kubectl's own layout:\n%s", got, want)
	}
}
