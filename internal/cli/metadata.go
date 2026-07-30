package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jzills/kx/internal/kubectl"
)

func sortStrings(values []string) { sort.Strings(values) }

// fetchMetadataField reads one metadata map (labels or annotations) off an
// indexed resource.
func fetchMetadataField(
	kubectl kubectl.Service, resolver IndexResolver, index int, field string,
) (keys []string, values map[string]string, err error) {
	name, namespace, kind, err := resolver.Fields(index)
	if err != nil {
		return nil, nil, err
	}
	raw, err := kubectl.Run([]string{"get", string(kind), name, "-n", namespace, "-o", "json"})
	if err != nil {
		return nil, nil, err
	}

	var object struct {
		Metadata map[string]json.RawMessage `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(raw), &object); err != nil {
		return nil, nil, err
	}

	values = map[string]string{}
	if encoded, ok := object.Metadata[field]; ok {
		if err := json.Unmarshal(encoded, &values); err != nil {
			return nil, nil, err
		}
	}
	keys = make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	// Sorted for stable output: kubectl returns a JSON object, and Go map
	// iteration would reorder the rows on every run.
	sortStrings(keys)
	return keys, values, nil
}

// MetadataReadCommand shows the labels or annotations on an indexed resource.
type MetadataReadCommand struct {
	Kubectl kubectl.Service
	State   IndexResolver
	// Field is the metadata key to read: "labels" or "annotations".
	Field string
}

func (c MetadataReadCommand) Execute(index int) ([]string, map[string]string, error) {
	return fetchMetadataField(c.Kubectl, c.State, index, c.Field)
}

var metadataVerbText = map[string]string{"label": "Labeled", "annotate": "Annotated"}

// MetadataWriteCommand sets or removes labels or annotations on an indexed
// resource.
type MetadataWriteCommand struct {
	Kubectl kubectl.Service
	State   IndexResolver
	// Verb is the kubectl subcommand: "label" or "annotate".
	Verb string
	// Field is the corresponding metadata key: "labels" or "annotations".
	Field string
}

func (c MetadataWriteCommand) Execute(
	index int, setKeys []string, sets map[string]string, removes []string, overwrite bool,
) (string, error) {
	if len(sets) == 0 && len(removes) == 0 {
		return "", fmt.Errorf("nothing to set or remove")
	}

	name, namespace, kind, err := c.State.Fields(index)
	if err != nil {
		return "", err
	}

	if !overwrite {
		// kubectl would refuse the write anyway, but its error names only the
		// first conflict; listing them all saves a round trip.
		_, current, err := fetchMetadataField(c.Kubectl, c.State, index, c.Field)
		if err != nil {
			return "", err
		}
		var conflicts []string
		for _, key := range setKeys {
			if _, exists := current[key]; exists {
				conflicts = append(conflicts, key)
			}
		}
		if len(conflicts) > 0 {
			return "", fmt.Errorf(
				"%s already set; use --overwrite to replace", strings.Join(conflicts, ", "),
			)
		}
	}

	args := []string{c.Verb, string(kind), name, "-n", namespace}
	for _, key := range setKeys {
		args = append(args, key+"="+sets[key])
	}
	for _, key := range removes {
		args = append(args, key+"-")
	}
	if overwrite {
		args = append(args, "--overwrite")
	}
	if _, err := c.Kubectl.Run(args); err != nil {
		return "", err
	}

	var parts []string
	if len(sets) > 0 {
		parts = append(parts, fmt.Sprintf("set %d", len(sets)))
	}
	if len(removes) > 0 {
		parts = append(parts, fmt.Sprintf("removed %d", len(removes)))
	}
	return fmt.Sprintf("%s %s/%s (%s)",
		metadataVerbText[c.Verb], kind, name, strings.Join(parts, ", ")), nil
}

// parsePairs splits "key=value" arguments, preserving the order they were given
// in so the resulting kubectl invocation is predictable.
func parsePairs(pairs []string) (keys []string, values map[string]string, err error) {
	values = map[string]string{}
	for _, pair := range pairs {
		key, value, found := strings.Cut(pair, "=")
		if !found || key == "" {
			return nil, nil, fmt.Errorf("expected key=value, got '%s'", pair)
		}
		if _, seen := values[key]; !seen {
			keys = append(keys, key)
		}
		values[key] = value
	}
	return keys, values, nil
}
