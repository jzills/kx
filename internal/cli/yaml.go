package cli

import (
	"sort"
	"strings"

	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
	"gopkg.in/yaml.v3"
)

// yamlOutputFormat is the format kx yaml's reply comes back in: the caller's
// own -o, or the -o yaml Execute adds when they named none.
func yamlOutputFormat(args []string) string {
	if !hasFlag(args, "--output", "-o") {
		return "yaml"
	}
	return outputFormat(args)
}

// manifestWriter writes the manifests kx yaml fetched, joined the way the
// format they came back in joins several of them.
//
// YAML's `---` is a document separator, not a general one, but it was written
// between every pair whatever the format. Between JSON objects it made the
// stream unparseable — `kx yaml 1 2 -o json | jq` failed on it, as did every
// other reader, since JSON has no multi-document form to separate. Between
// -o name lines or a Go template's output it was simply a line the caller
// never asked for and would have had to filter back out.
//
// So only YAML is separated. JSON is merged into the one List kubectl itself
// answers several names with, and anything else is written as it came.
type manifestWriter struct {
	// merge holds the replies back to combine them, rather than writing
	// each as it arrives.
	merge     bool
	separator string
	replies   []string
	written   bool
}

func newManifestWriter(args []string, targets int) *manifestWriter {
	writer := &manifestWriter{}
	switch yamlOutputFormat(args) {
	case "yaml":
		writer.separator = "---"
	case "json":
		// One target keeps kubectl's own shape for one name — the bare
		// object, not a List holding it. Decided by what was asked for
		// rather than by how many replies arrive, so a fetch that fails
		// partway still prints the shape the arguments promised.
		writer.merge = targets > 1
	}
	return writer
}

// separate writes the separator owed before the next manifest, and is called
// before that manifest's banner rather than beside the manifest itself.
//
// The separator goes to stdout and the banner to stderr, so on a terminal the
// two interleave in call order. Written beside the manifest, that order read
// `banner / --- / manifest`: the separator sat between a banner and the
// manifest it names, while the first banner sat directly above its own. Each
// banner now introduces the manifest under it, whichever pair it is.
func (w *manifestWriter) separate() {
	if w.merge || w.separator == "" || !w.written {
		return
	}
	render.Raw(w.separator)
}

func (w *manifestWriter) add(manifest string) {
	if w.merge {
		w.replies = append(w.replies, manifest)
		return
	}
	w.written = true
	// Trimmed: Raw ends the line itself, so a manifest's own trailing
	// newline would leave a blank line between every pair.
	render.Raw(strings.TrimRight(manifest, "\n"))
}

// close writes what merge held back. Nothing when every fetch failed: an
// empty List reads as a successful query that matched nothing, which is not
// what happened.
func (w *manifestWriter) close() {
	if !w.merge || len(w.replies) == 0 {
		return
	}
	merged, ok := mergeCollections(w.replies, false)
	if !ok {
		// Not documents kx can read, so there is nothing to merge them
		// into. Written as they came, as kx did before.
		for _, reply := range w.replies {
			render.Raw(strings.TrimRight(reply, "\n"))
		}
		return
	}
	render.Raw(strings.TrimRight(merged, "\n"))
}

// sortedKeys returns a map's keys in a stable order.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// YamlCommand prints an indexed resource's manifest, optionally narrowed to
// named fields.
type YamlCommand struct {
	Kubectl kubectl.Service
	State   IndexResolver
}

func (c YamlCommand) Execute(ref state.Ref, show []string, extraArgs []string) (string, error) {
	name, namespace, kind, err := c.State.Resolve(ref)
	if err != nil {
		return "", err
	}
	args := []string{"get", string(kind), name, "-n", namespace}
	// kx's own -o yaml only when the caller named no format. Appended
	// unconditionally it arrived beside the caller's, and kubectl resolved the
	// pair by taking the last — so `kx yaml 1 -o json` worked by accident and
	// would have broken the moment the order changed.
	if !hasFlag(extraArgs, "--output", "-o") {
		args = append(args, "-o", "yaml")
	}
	raw, err := c.Kubectl.Run(append(args, extraArgs...))
	if err != nil {
		return "", err
	}
	if len(show) == 0 {
		return raw, nil
	}

	var document any
	if err := yaml.Unmarshal([]byte(raw), &document); err != nil {
		return "", err
	}
	found := findKeys(document, show)
	return encodeYAML(found)
}

// encodeYAML marshals at two-space indentation. yaml.v3 defaults to four,
// which would make `kx yaml --show` disagree with both the manifest kubectl
// printed and every other YAML the user sees.
func encodeYAML(value any) (string, error) {
	var out strings.Builder
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// findKeys collects the value of each requested key from anywhere in the
// manifest, preferring the shallowest occurrence when a key appears at several
// depths.
//
// A breadth-first walk guarantees shallowest-wins: a workload's own top-level
// `metadata` is returned rather than its pod template's nested `metadata`,
// while genuinely nested-only keys (`containerStatuses` under `status`) are
// still found. A matched key's own subtree is not descended into, so asking for
// `metadata` does not also pull the `metadata` inside it.
func findKeys(document any, keys []string) map[string]any {
	wanted := make(map[string]bool, len(keys))
	for _, key := range keys {
		wanted[key] = true
	}

	result := map[string]any{}
	frontier := []any{document}
	for len(frontier) > 0 {
		var deeper []any
		for _, node := range frontier {
			switch value := node.(type) {
			case map[string]any:
				// Walked in sorted order because Go randomises map iteration:
				// an unordered walk puts sibling subtrees into the frontier in
				// a different order on every run, so a key present in two of
				// them at the same depth resolves differently each time and
				// `kx yaml --show` prints a different document for the same
				// manifest.
				for _, key := range sortedKeys(value) {
					child := value[key]
					if wanted[key] {
						if _, seen := result[key]; !seen {
							result[key] = child
						}
						continue
					}
					deeper = append(deeper, child)
				}
			case []any:
				deeper = append(deeper, value...)
			}
		}
		frontier = deeper
	}
	return result
}
