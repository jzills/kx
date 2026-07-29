package cli

import (
	"sort"
	"strings"

	"github.com/jzills/kx/internal/kubectl"
	"gopkg.in/yaml.v3"
)

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

func (c YamlCommand) Execute(index int, show []string) (string, error) {
	name, namespace, kind, err := c.State.Fields(index)
	if err != nil {
		return "", err
	}
	raw, err := c.Kubectl.Run([]string{"get", string(kind), name, "-n", namespace, "-o", "yaml"})
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
