package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/jzills/kx/internal/index"
)

// narrowCollection keeps the items of a -o json or -o yaml collection whose
// metadata.name matches term, leaving the document around them as it came.
//
// kubectl answers a collection request with a List whose items each carry
// metadata.name, so there are rows to narrow — they are simply not in a
// table. The refusal this replaces said "that output has no rows for it to
// pick", which is true of a projection and not of a collection.
//
// A term matching nothing leaves "items": [] rather than no document, so a
// reader does not have to tell "narrowed to nothing" from "kx printed
// nothing".
func narrowCollection(output, term string, yamlFormat bool) (string, error) {
	if yamlFormat {
		return narrowYAMLCollection(output, term)
	}
	return narrowJSONCollection(output, term)
}

// narrowJSONCollection filters a JSON List's items.
//
// Decoded into json.RawMessage so each item and every sibling field keeps the
// bytes kubectl sent, and re-encoded at kubectl's own four-space indent.
// Go sorts map keys when it marshals, which is the order kubectl already
// prints — apiVersion, items, kind, metadata — so the document comes back
// looking as it went in.
func narrowJSONCollection(output, term string) (string, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &doc); err != nil {
		return "", notACollectionError("-o json")
	}
	raw, ok := doc["items"]
	if !ok {
		return "", notACollectionError("-o json")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return "", notACollectionError("-o json")
	}
	matches := index.NameMatcher(term)
	kept := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		var named struct {
			Metadata struct{ Name string } `json:"metadata"`
		}
		if err := json.Unmarshal(item, &named); err != nil {
			return "", notACollectionError("-o json")
		}
		if matches(named.Metadata.Name) {
			kept = append(kept, item)
		}
	}
	narrowed, err := json.Marshal(kept)
	if err != nil {
		return "", err
	}
	doc["items"] = narrowed
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetIndent("", "    ")
	if err := encoder.Encode(doc); err != nil {
		return "", err
	}
	return out.String(), nil
}

// narrowYAMLCollection filters a YAML List's items through yaml.Node, which
// keeps the document's key order and style rather than rebuilding it from a
// map — kubectl's own order is what the caller expects to diff against.
func narrowYAMLCollection(output, term string) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(output), &doc); err != nil {
		return "", notACollectionError("-o yaml")
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return "", notACollectionError("-o yaml")
	}
	root := doc.Content[0]
	items := mappingValue(root, "items")
	if items == nil || items.Kind != yaml.SequenceNode {
		return "", notACollectionError("-o yaml")
	}
	matches := index.NameMatcher(term)
	kept := make([]*yaml.Node, 0, len(items.Content))
	for _, item := range items.Content {
		metadata := mappingValue(item, "metadata")
		if metadata == nil {
			return "", notACollectionError("-o yaml")
		}
		name := mappingValue(metadata, "name")
		if name == nil {
			return "", notACollectionError("-o yaml")
		}
		if matches(name.Value) {
			kept = append(kept, item)
		}
	}
	items.Content = kept
	// An emptied sequence prints as "items: []" rather than a dangling key.
	if len(kept) == 0 {
		items.Style = yaml.FlowStyle
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return "", err
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// mappingValue is the value node for key in a mapping node, or nil.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// notACollectionError is for a reply in a collection format that is not a
// collection — a single named resource, or something kx cannot read as one.
// Narrowing one resource by name is a question that answers itself, so the
// refusal points at dropping the flag rather than at a different spelling.
func notACollectionError(format string) error {
	return fmt.Errorf("'--match' narrows the items of a collection, and kubectl's '%s' reply "+
		"is not one — it is a single resource, or a shape kx cannot read. Drop the flag.", format)
}

// projectionMatchError refuses a --match term beside output the caller shaped
// themselves. Unlike a collection, a projection may carry no names at all —
// `-o jsonpath={.items[*].status.phase}` has none — so there is nothing kx
// can key on, whatever the reply happens to contain.
func projectionMatchError(format string) error {
	return errors.New("'--match' narrows by each item's name, and '-o " + format +
		"' is a shape you chose, which kx cannot assume has names in it. " +
		"Narrow the template itself, or select with -l instead.")
}
