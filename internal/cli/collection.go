package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"go.yaml.in/yaml/v2"

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
//
// Never through json.Marshal, which escapes &, < and > for HTML: an
// annotation holding a URL came back with \u0026 where kubectl printed &.
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
	narrowed, err := encodeCollectionJSON(kept, "")
	if err != nil {
		return "", err
	}
	doc["items"] = json.RawMessage(narrowed)
	return encodeCollectionJSON(doc, "    ")
}

// encodeCollectionJSON is v as JSON, indented by indent, with &, < and > left
// as they are rather than escaped for HTML.
func encodeCollectionJSON(v any, indent string) (string, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", indent)
	if err := encoder.Encode(v); err != nil {
		return "", err
	}
	return out.String(), nil
}

// narrowYAMLCollection filters a YAML List's items, keeping the document's key
// order — kubectl's own order is what the caller expects to diff against.
//
// Through yaml.v2's MapSlice, which is what kubectl prints YAML with
// (sigs.k8s.io/yaml), so the narrowed document is laid out exactly as kubectl
// lays it out. yaml.v3 indented a sequence's dashes beneath its key and never
// folded a long string, where kubectl folds at 80 columns, so a narrowed
// document differed from kubectl's on every item line.
func narrowYAMLCollection(output, term string) (string, error) {
	var doc yaml.MapSlice
	if err := yaml.Unmarshal([]byte(output), &doc); err != nil {
		return "", notACollectionError("-o yaml")
	}
	at := -1
	for i, entry := range doc {
		if entry.Key == "items" {
			at = i
		}
	}
	if at < 0 {
		return "", notACollectionError("-o yaml")
	}
	items, ok := doc[at].Value.([]any)
	if !ok && doc[at].Value != nil {
		return "", notACollectionError("-o yaml")
	}
	matches := index.NameMatcher(term)
	var kept []any
	for _, item := range items {
		metadata, ok := mapSliceValue(item, "metadata").(yaml.MapSlice)
		if !ok {
			return "", notACollectionError("-o yaml")
		}
		name := mapSliceValue(metadata, "name")
		if name == nil {
			return "", notACollectionError("-o yaml")
		}
		if matches(fmt.Sprint(name)) {
			kept = append(kept, item)
		}
	}
	doc[at].Value = kept
	return encodeCollectionYAML(doc)
}

// encodeCollectionYAML is v as YAML laid out as kubectl prints it: yaml.v2's
// encoder, which kubectl prints with.
func encodeCollectionYAML(v any) (string, error) {
	out, err := yaml.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// mapSliceValue is the value for key in a YAML mapping, or nil when node is
// not a mapping or has no such key.
func mapSliceValue(node any, key string) any {
	mapping, ok := node.(yaml.MapSlice)
	if !ok {
		return nil
	}
	for _, entry := range mapping {
		if entry.Key == key {
			return entry.Value
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
