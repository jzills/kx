package cli

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

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

// markNamePattern is the set of characters a mark name may use: the ones a
// shell and a URL both pass through unescaped, so a mark can be typed on a
// command line or dropped into a kubectl invocation without quoting.
var markNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// validMarkName accepts the names a user has to type repeatedly, and refuses
// a purely numeric one: '@3' beside index 3 is an ambiguity nobody needs.
func validMarkName(name string) error {
	if name == "" {
		return fmt.Errorf("A mark needs a name — try 'kx mark api 3'.")
	}
	if _, err := strconv.Atoi(name); err == nil {
		return fmt.Errorf(
			"'%s' is a number, which a mark name cannot be — '@%s' would be indistinguishable from index %s.",
			name, name, name)
	}
	if !markNamePattern.MatchString(name) {
		return fmt.Errorf(
			"'%s' is not a valid mark name — letters, digits, '-', '_' and '.' only.", name)
	}
	return nil
}

// parseRef parses one argument into a reference: a leading '@' names a mark,
// validated the same way parseRefs validates one, and anything else is a
// plain index. It exists so the single-reference commands (edit, exec, tree,
// diagnose, node cordon/drain, ...) stop passing a bare int around, and it is
// the other place — beside parseRefs — that must never build a Ref carrying
// both an Index and a Mark: the two branches below are mutually exclusive by
// construction, not by convention.
func parseRef(name, arg string) (state.Ref, error) {
	if mark, ok := strings.CutPrefix(arg, "@"); ok {
		if err := validMarkName(mark); err != nil {
			return state.Ref{}, err
		}
		return state.Ref{Mark: mark}, nil
	}
	index, err := parseIndex(name, arg)
	if err != nil {
		return state.Ref{}, err
	}
	return state.Ref{Index: index}, nil
}

// parseRefs turns argv into the references a command acts on: a leading '@'
// makes a mark reference, and everything else parses as an index or range,
// expanding to one state.Ref{Index: n} per index and dropping repeats.
//
// An index named twice is one resource, and the repeat is nearly always
// accidental: overlapping ranges ("1..3 2..4") are how it actually happens,
// and they printed 2 and 3 twice — or, for kx delete, asked kubectl to delete
// something already gone. First occurrence wins, so the order the user wrote
// survives; a mark is deduped the same way, by name, since "@api @api" is
// one reference twice for the same reason a repeated index is. Dropped
// silently: the output shows each resource once, which says it.
//
// This is a dedupe by argument, before anything resolves — it only catches a
// reference spelled twice. resolveIndexes (below) dedupes again afterward, by
// what each reference resolved to, which is the dedupe that actually
// matters: an index and a mark can name the same resource. The dedupe here
// remains only so a repeated argument is not resolved twice for no reason.
//
// A mark at either end of a range is refused before expandRange ever runs —
// checked here rather than left for parseRef/parseIndex to fail on, because
// "1..@api" IS shaped like a range (it contains ".."), and expandRange would
// otherwise report the generic "not a valid range" instead of naming why: a
// mark names one resource, and a range is positional by definition, so the
// two cannot be combined in either spelling or on either side.
func parseRefs(resolver IndexResolver, name string, args []string) ([]state.Ref, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("Missing argument '%s'.", name)
	}
	refs := make([]state.Ref, 0, len(args))
	seenIndex := make(map[int]bool, len(args))
	seenMark := make(map[string]bool, len(args))
	keep := func(candidates ...int) {
		for _, index := range candidates {
			if seenIndex[index] {
				continue
			}
			seenIndex[index] = true
			refs = append(refs, state.Ref{Index: index})
		}
	}
	for _, arg := range args {
		if strings.Contains(arg, "..") && strings.Contains(arg, "@") {
			return nil, fmt.Errorf(
				"Invalid value for '%s': '%s' is not a valid range — a mark names one "+
					"resource, so it cannot be either end of a range.", name, arg)
		}
		if mark, ok := strings.CutPrefix(arg, "@"); ok {
			if err := validMarkName(mark); err != nil {
				return nil, err
			}
			if seenMark[mark] {
				continue
			}
			seenMark[mark] = true
			refs = append(refs, state.Ref{Mark: mark})
			continue
		}
		if expanded, ok, err := expandRange(resolver, name, arg); ok {
			if err != nil {
				return nil, err
			}
			keep(expanded...)
			continue
		}
		index, err := parseIndex(name, arg)
		if err != nil {
			return nil, err
		}
		keep(index)
	}
	return refs, nil
}

// resolveRefs parses a command's resource arguments and resolves every one of
// them before the command acts on any.
//
// It replaces separate parse-then-validate calls, which were two calls only
// two of the five parsing files made together — kx cordon, kx ref and the
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
//
// Each parsed Ref is passed to resolve exactly as parseRefs built it — never
// rebuilt from ref.Index — so a mark parsed here is a mark resolve sees. That
// used to be safe to get wrong because every Ref was an index; now a
// reconstruction would silently turn "@api" into index 0.
func resolveIndexes(
	resolver IndexResolver, name string, args []string,
	resolve func(ref state.Ref) (name, namespace string, kind kinds.Kind, err error),
) ([]Resolved, error) {
	refs, err := parseRefs(resolver, name, args)
	if err != nil {
		return nil, err
	}
	resolved := make([]Resolved, 0, len(refs))
	seen := make(map[Resolved]bool, len(refs))
	for _, ref := range refs {
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
