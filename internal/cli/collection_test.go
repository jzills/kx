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

// kubectl folds a long plain string at 80 columns. yaml.v3 never folds, so a
// narrowed document unfolded every long annotation kubectl had folded. Taken
// from kubectl's own output for kube-root-ca.crt.
func TestNarrowYAMLCollectionKeepsKubectlsFolding(t *testing.T) {
	item := "- apiVersion: v1\n" +
		"  kind: ConfigMap\n" +
		"  metadata:\n" +
		"    annotations:\n" +
		"      kubernetes.io/description: Contains a CA bundle that can be used to verify the\n" +
		"        kube-apiserver when using internal endpoints such as the internal service\n" +
		"        IP or kubernetes.default.svc. No other usage is guaranteed across distributions\n" +
		"        of Kubernetes clusters.\n" +
		"    name: kube-root-ca.crt\n"
	reply := "apiVersion: v1\nitems:\n" + item +
		"- apiVersion: v1\n  kind: ConfigMap\n  metadata:\n    name: coredns\n" +
		"kind: List\nmetadata:\n  resourceVersion: \"\"\n"
	want := "apiVersion: v1\nitems:\n" + item + "kind: List\nmetadata:\n  resourceVersion: \"\"\n"

	got, err := narrowCollection(reply, "root", true)
	if err != nil {
		t.Fatalf("narrowCollection: %v", err)
	}
	if got != want {
		t.Errorf("narrowed document =\n%s\nwant kubectl's own layout:\n%s", got, want)
	}
}

// A term matching nothing leaves "items: []", as kubectl prints an empty
// List, rather than "items: null" or no key.
func TestNarrowYAMLCollectionToNothingLeavesAnEmptyList(t *testing.T) {
	reply := "apiVersion: v1\nitems:\n- metadata:\n    name: web-1\nkind: List\n"
	got, err := narrowCollection(reply, "zzz", true)
	if err != nil {
		t.Fatalf("narrowCollection: %v", err)
	}
	if want := "apiVersion: v1\nitems: []\nkind: List\n"; got != want {
		t.Errorf("narrowed document = %q, want %q", got, want)
	}
}
