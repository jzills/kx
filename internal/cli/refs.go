package cli

import (
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// IndexResolver is the slice of the state service the reference-taking
// commands need: turning a reference into the resource it names.
type IndexResolver interface {
	Resolve(ref state.Ref) (name, namespace string, kind kinds.Kind, err error)
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
	indexes, err := parseIndexes(resolver, name, args)
	if err != nil {
		return nil, err
	}
	resolved := make([]Resolved, 0, len(indexes))
	seen := make(map[Resolved]bool, len(indexes))
	for _, index := range indexes {
		ref := state.Ref{Index: index}
		resourceName, namespace, kind, err := resolver.Resolve(ref)
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
