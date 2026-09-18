package cli

import (
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// IndexResolver is the slice of the state service the reference-taking
// commands need: turning a reference into the resource it names.
type IndexResolver interface {
	Resolve(ref state.Ref) (name, namespace string, kind kinds.Kind, err error)
	// ResolveExpecting is Resolve for a caller that has already named the kind
	// it wants, so a failure — out of range, no state, or the wrong kind — is
	// reported against that kind rather than against whatever listing happens
	// to be current.
	ResolveExpecting(ref state.Ref, expected kinds.Kind) (name, namespace string, err error)
	Fields(index int) (name, namespace string, kind kinds.Kind, err error)
	// Count returns how many resources are in the current listing, used to
	// resolve the open end of a "5.." range and to trim a closed one.
	Count() (int, error)
}

// Resolved is a reference together with what it resolved to, so a command that
// has parsed its arguments does not have to ask again.
type Resolved struct {
	Ref       state.Ref
	Name      string
	Namespace string
	Kind      kinds.Kind
}

// resolveRefs parses a command's resource arguments and resolves every one of
// them before the command acts on any.
//
// It replaces parseIndexes followed by validateIndexes, which were two calls
// only two of the five parsing files made together — kx cordon, kx ref and the
// kx get relist resolved as they went, so a bad reference late in a batch left
// the earlier ones already acted on. kx delete's own test has asserted the
// all-or-nothing guarantee since it was written; this is where every command
// gets it.
//
// Dedupe is by resolved identity rather than by the argument as written: two
// spellings of one resource are one resource, which an []int could not see.
// First occurrence wins, so the order the user wrote survives.
func resolveRefs(resolver IndexResolver, name string, args []string) ([]Resolved, error) {
	return resolveIndexes(resolver, name, args, resolver.Resolve)
}

// resolveRefsExpecting is resolveRefs for a caller that has already named the
// kind it wants — the kx get relist, so far. It resolves through
// ResolveExpecting rather than Resolve, so every failure (out of range, no
// state, or the wrong kind) is reported against the kind named on the command
// line, the way FieldsExpecting always has. Parsing and identity-dedupe are
// shared with resolveRefs via resolveIndexes rather than duplicated.
func resolveRefsExpecting(
	resolver IndexResolver, name string, args []string, expected kinds.Kind,
) ([]Resolved, error) {
	return resolveIndexes(resolver, name, args, func(ref state.Ref) (string, string, kinds.Kind, error) {
		resourceName, namespace, err := resolver.ResolveExpecting(ref, expected)
		return resourceName, namespace, expected, err
	})
}

// resolveIndexes parses a command's resource arguments and resolves every one
// of them through resolve, deduping by resolved identity before any of them
// is acted on. Shared by resolveRefs and resolveRefsExpecting so the two
// differ only in how a single reference resolves, not in how a batch is
// parsed or deduped.
func resolveIndexes(
	resolver IndexResolver, name string, args []string,
	resolve func(ref state.Ref) (name, namespace string, kind kinds.Kind, err error),
) ([]Resolved, error) {
	indexes, err := parseIndexes(resolver, name, args)
	if err != nil {
		return nil, err
	}
	resolved := make([]Resolved, 0, len(indexes))
	seen := make(map[Resolved]bool, len(indexes))
	for _, index := range indexes {
		ref := state.Ref{Index: index}
		resourceName, namespace, kind, err := resolve(ref)
		if err != nil {
			return nil, err
		}
		identity := Resolved{Name: resourceName, Namespace: namespace, Kind: kind}
		if seen[identity] {
			continue
		}
		seen[identity] = true
		entry := identity
		entry.Ref = ref
		resolved = append(resolved, entry)
	}
	return resolved, nil
}

// indexesOf returns the underlying indexes of a resolved batch, for callers
// that have not yet converted from []int — such as refuseScopeFlagForIndexes,
// which converts alongside the single-reference commands in a later PR.
func indexesOf(resolved []Resolved) []int {
	indexes := make([]int, len(resolved))
	for i, target := range resolved {
		indexes[i] = target.Ref.Index
	}
	return indexes
}
