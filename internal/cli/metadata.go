package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/state"
)

func sortStrings(values []string) { sort.Strings(values) }

// fetchMetadataField reads one metadata map (labels or annotations) off a
// referenced resource.
// One reference, through the same fetch the batch uses — the reply shapes and
// the sorting are parsed in exactly one place.
func fetchMetadataField(
	kubectl kubectl.Service, resolver IndexResolver, ref state.Ref, field string,
) (keys []string, values map[string]string, err error) {
	name, namespace, kind, err := resolver.Resolve(ref)
	if err != nil {
		return nil, nil, err
	}
	resolved := []Resolved{{Ref: ref, Name: name, Namespace: namespace, Kind: kind}}
	results, err := fetchMetadataFields(kubectl, resolved, field)
	if err != nil {
		return nil, nil, err
	}
	return results[ref].keys, results[ref].values, nil
}

// metadataGroup is the resources of one kind in one namespace that a batched
// read asks about — the most kubectl can fetch in a single call.
type metadataGroup struct {
	kind, namespace string
	names           []string
	refs            []state.Ref
}

// metadataResult is one resource's metadata field, ready to render.
type metadataResult struct {
	keys   []string
	values map[string]string
}

// fetchMetadataFields reads one metadata field for several indexes, in one
// kubectl call per (kind, namespace) rather than one per index.
//
// Measured on a 14-pod listing: fourteen serial calls took 1.12s where one
// batched call returned the same data in 0.079s, and every call pays the round
// trip again on a remote cluster. kubectl can fetch several names at once but
// neither several namespaces nor several kinds, so the indexes are grouped —
// which a spanning listing (kx get -A) and a mixed one (a tree walk) both
// need.
//
// Replies are matched by metadata.name, not by position. kubectl happens to
// return items in the order asked for, but nothing in its contract says so,
// and a mismatch would attribute one resource's labels to another with nothing
// on screen to give it away.
func fetchMetadataFields(
	kubectl kubectl.Service, resolved []Resolved, field string,
) (map[state.Ref]metadataResult, error) {
	var groups []*metadataGroup
	byKey := map[string]*metadataGroup{}
	for _, target := range resolved {
		key := string(target.Kind) + "\x00" + target.Namespace
		existing, ok := byKey[key]
		if !ok {
			existing = &metadataGroup{kind: string(target.Kind), namespace: target.Namespace}
			byKey[key] = existing
			groups = append(groups, existing)
		}
		existing.names = append(existing.names, target.Name)
		existing.refs = append(existing.refs, target.Ref)
	}

	results := make(map[state.Ref]metadataResult, len(resolved))
	for _, g := range groups {
		byName, sole, err := g.read(kubectl, field)
		if err != nil {
			return nil, err
		}
		for i, ref := range g.refs {
			values, ok := byName[g.names[i]]
			// Asked for one name, kubectl either errored or answered about
			// that resource, so the sole object is it — whether or not the
			// reply carries a metadata.name to match on. The name match is
			// there to tell several replies apart, and with one there is
			// nothing to tell apart.
			if !ok && len(g.names) == 1 && sole != nil {
				values, ok = sole, true
			}
			if !ok {
				// kubectl answered without it, which means it is gone: a
				// stale index, and what withRefresh exists to recover from.
				// Rendering it as a resource with no labels would read as a
				// fact about the resource instead.
				return nil, StaleResourceError{
					Kind: kinds.Kind(g.kind), Name: g.names[i], Namespace: g.namespace, Ref: ref,
				}
			}
			results[ref] = newMetadataResult(values)
		}
	}
	return results, nil
}

// read fetches the group's metadata, in one API request where it can.
//
// kubectl makes a request per named resource when handed several names — 14
// names measured 892ms against 97ms for the same fetch with none — so a group
// of more than one asks for the collection and filters here. The extra objects
// come back in the same single response that the named form would have paid a
// round trip each for.
//
// One name stays a named fetch: listing a whole namespace to find one resource
// is the slower half of that trade.
//
// A collection fetch needs list permission where a named get needs only get,
// and RBAC granting the second without the first is ordinary — so a failed
// listing falls back to naming each resource rather than turning a working
// command into a permission error. The fallback's error is the one reported
// when both fail, because it names the resource where the listing's names only
// the collection.
func (g *metadataGroup) read(
	kubectl kubectl.Service, field string,
) (byName map[string]map[string]string, sole map[string]string, err error) {
	if len(g.names) > 1 {
		raw, listErr := kubectl.Run([]string{
			"get", g.kind, "-n", g.namespace, "-o", "json",
		})
		if listErr == nil {
			return metadataByName(raw, field)
		}
	}
	byName = map[string]map[string]string{}
	for _, name := range g.names {
		raw, nameErr := kubectl.Run([]string{
			"get", g.kind, name, "-n", g.namespace, "-o", "json",
		})
		if nameErr != nil {
			return nil, nil, nameErr
		}
		single, singleSole, parseErr := metadataByName(raw, field)
		if parseErr != nil {
			return nil, nil, parseErr
		}
		if values, ok := single[name]; ok {
			byName[name] = values
			continue
		}
		if singleSole != nil {
			byName[name] = singleSole
		}
	}
	if len(g.names) == 1 {
		for _, values := range byName {
			sole = values
		}
	}
	return byName, sole, nil
}

// metadataByName reads the field off every object in a kubectl reply, keyed by
// name.
//
// Two shapes, because kubectl wraps a reply in a List only when it was asked
// for several names: one name comes back as the bare object.
// sole is the field off the only object in the reply, for the single-name case
// that needs no name to match on; nil when the reply held more than one.
func metadataByName(
	raw, field string,
) (byName map[string]map[string]string, sole map[string]string, err error) {
	type object struct {
		Metadata map[string]json.RawMessage `json:"metadata"`
	}
	var reply struct {
		Items *[]object `json:"items"`
		object
	}
	if err := json.Unmarshal([]byte(raw), &reply); err != nil {
		return nil, nil, err
	}
	objects := []object{reply.object}
	if reply.Items != nil {
		objects = *reply.Items
	}

	byName = map[string]map[string]string{}
	for _, item := range objects {
		var name string
		if encoded, ok := item.Metadata["name"]; ok {
			if err := json.Unmarshal(encoded, &name); err != nil {
				return nil, nil, err
			}
		}
		values := map[string]string{}
		if encoded, ok := item.Metadata[field]; ok {
			if err := json.Unmarshal(encoded, &values); err != nil {
				return nil, nil, err
			}
		}
		byName[name] = values
	}
	if len(objects) == 1 {
		sole = byName[""]
		for _, values := range byName {
			sole = values
		}
	}
	return byName, sole, nil
}

// newMetadataResult sorts the keys for stable output: kubectl returns a JSON
// object, and Go map iteration would reorder the rows on every run.
func newMetadataResult(values map[string]string) metadataResult {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sortStrings(keys)
	return metadataResult{keys: keys, values: values}
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

// Execute writes the change. extraArgs are kubectl's own flags, forwarded
// after kx's.
func (c MetadataWriteCommand) Execute(
	ref state.Ref, setKeys []string, sets map[string]string, removes []string, overwrite bool,
	extraArgs []string,
) (string, error) {
	if len(sets) == 0 && len(removes) == 0 {
		message := fmt.Sprintf(
			"kx %s needs key=value to set, or --remove to name a key to drop — neither was given.",
			c.Verb)
		// Pairs are read only from before the first kubectl flag, so one typed
		// after them lands here, and "neither was given" alone would deny what
		// the user can see they typed.
		if len(extraArgs) > 0 {
			message += " key=value pairs go right after the index, before kubectl's flags."
		}
		return "", errors.New(message)
	}

	name, namespace, kind, err := c.State.Resolve(ref)
	if err != nil {
		return "", err
	}

	if !overwrite {
		// kubectl would refuse the write anyway, but its error names only the
		// first conflict; listing them all saves a round trip.
		_, current, err := fetchMetadataField(c.Kubectl, c.State, ref, c.Field)
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
	args = append(args, extraArgs...)
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
	// kx replaces kubectl's output with this line, so it has to say when
	// nothing was changed — as kx delete's does.
	if isDryRun(extraArgs) {
		parts = append(parts, "dry run — nothing was changed")
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
