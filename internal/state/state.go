// Package state persists the resource listing produced by `kx get` as a
// history stack, and resolves indexes against the current entry.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
)

// Resource is one indexed row: the name kubectl reported, the kind it was
// listed as, and the namespace it lives in.
//
// Namespace is carried per resource, not just per entry, because a listing can
// span namespaces — `kx get -A` is exactly that — and an index has to resolve
// to the one place its resource actually is. It is omitted for the ordinary
// single-namespace listing, whose namespace the entry already records; see
// Service.Fields for the fallback that makes both shapes resolve.
type Resource struct {
	Name      string     `json:"name"`
	Kind      kinds.Kind `json:"kind"`
	Namespace string     `json:"namespace,omitempty"`
}

// Resources is an ordered name→kind mapping.
//
// Order is load-bearing: an index resolves to the nth entry, so this must
// preserve the order names were listed in and the order they appear in the
// JSON array on disk. That rules out a Go map, whose iteration order is
// randomized — a map here would resolve indexes to different resources on
// different runs. It also rules out keying the on-disk JSON by name (an
// earlier shape did this): a name is not unique across kinds, so two
// resources of different kinds sharing a name could not both be represented.
// A version mismatch on the on-disk shape is handled by the schema-version
// check in loadHistory, not by this type trying to stay eternally
// compatible with every historical shape.
type Resources struct {
	entries []Resource
}

// NewResources builds an ordered mapping from parallel names and a single kind,
// which is the shape `kx get` produces.
func NewResources(names []string, kind kinds.Kind) Resources {
	entries := make([]Resource, 0, len(names))
	for _, name := range names {
		entries = append(entries, Resource{Name: name, Kind: kind})
	}
	return Resources{entries: entries}
}

// NewOrderedResources builds an ordered mapping from name/kind pairs already
// in index order, which is what a tree walk produces.
func NewOrderedResources(entries []Resource) Resources {
	ordered := make([]Resource, len(entries))
	copy(ordered, entries)
	return Resources{entries: ordered}
}

// Entries returns the resources in index order.
func (r Resources) Entries() []Resource { return r.entries }

// Spanning reports whether the listing records namespaces per resource, which
// is what an all-namespace listing produces and what distinguishes "this entry
// has no single namespace" from "this entry's namespace went unrecorded".
func (r Resources) Spanning() bool {
	for _, e := range r.entries {
		if e.Namespace != "" {
			return true
		}
	}
	return false
}

// Len returns the number of indexed resources.
func (r Resources) Len() int { return len(r.entries) }

// Names returns the resource names in index order, satisfying index.Resolver.
func (r Resources) Names() []string {
	names := make([]string, 0, len(r.entries))
	for _, e := range r.entries {
		names = append(names, e.Name)
	}
	return names
}

// Kind returns the kind recorded for the first entry named name. Ambiguous
// when two entries share a name — returns whichever was inserted first.
// Callers that already know the entry's position (having resolved it via
// index.Resolve) should use At instead; this is for callers, such as tests,
// that know the name is unique in the listing.
func (r Resources) Kind(name string) (kinds.Kind, bool) {
	for _, e := range r.entries {
		if e.Name == name {
			return e.Kind, true
		}
	}
	return "", false
}

// At returns the entry at a 1-based index, the same convention index.Resolve
// uses, so a caller that has already resolved a name via index.Resolve can
// look up its kind without a second, ambiguous, name-keyed search.
func (r Resources) At(index int) (Resource, bool) {
	i := index - 1
	if i < 0 || i >= len(r.entries) {
		return Resource{}, false
	}
	return r.entries[i], true
}

// MarshalJSON writes entries as a JSON array, in index order.
func (r Resources) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.entries)
}

// UnmarshalJSON reads a JSON array. Array order is part of the JSON spec, so
// unlike the object shape this replaced, no custom decoding is needed to
// preserve it.
func (r *Resources) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &r.entries)
}

// Query is the `kx get` invocation that produced a state entry, kept so a
// stale entry can be re-run. Nil for entries not created by `kx get`
// (tree --index, pre-existing state files).
type Query struct {
	Resource string   `json:"resource"`
	Args     []string `json:"args"`
	Match    *string  `json:"match"`
}

// State is one history entry: an indexed listing, the namespace it came from,
// and the kubeconfig context it was taken against.
//
// Context is what stops an index counted in one cluster from being spent in
// another: names repeat across clusters, so without it `kx get deploy` in
// staging followed by a context switch leaves every index silently pointing at
// a prod resource of the same name. Empty means unknown — a kubeconfig with no
// current context — and is never treated as a mismatch.
type State struct {
	Resources Resources `json:"resources"`
	Namespace string    `json:"namespace"`
	Query     *Query    `json:"query"`
	Context   string    `json:"context,omitempty"`
}

// Names satisfies index.Resolver.
func (s State) Names() []string { return s.Resources.Names() }

// currentSchemaVersion is the on-disk shape's version. A plain, monotonically
// increasing int — a version mismatch always means "reset", so there is no
// partial-compatibility case for semver's major/minor/patch semantics to
// express.
//
// 2 added State.Context and Resource.Namespace. Both are additive, and a
// version 1 file would decode without error, but it would decode wrong: an
// absent context reads as "unknown", which is the value that waives the
// cluster check, so every pre-upgrade entry would be trusted in whatever
// context the user happens to be in now. Resetting once is the cheaper answer.
const currentSchemaVersion = 2

// History is the stack of listings with a cursor marking the current entry,
// plus the per-kind slots that sit outside the stack.
type History struct {
	Version int     `json:"version"`
	States  []State `json:"states"`
	Cursor  int     `json:"cursor"`
	// Named holds the most recent listing of each slotted kind, keyed by kind.
	// It is deliberately outside States: `kx ns` runs often enough that pushing
	// every namespace listing onto the stack evicted the work the stack is for,
	// and a slot cannot be displaced by MaxHistory. Absent in files written
	// before slots existed, which read as an empty map.
	Named map[kinds.Kind]State `json:"named,omitempty"`
}

// ErrNoState is returned when no state file exists yet.
var ErrNoState = errors.New("No state found. Run `kx get <resource>` first.")

// ContextMismatchError reports that an index was counted in one kubeconfig
// context and is being spent in another.
//
// This is not a stale listing — the resource it names may well exist here — and
// that is exactly why it has to be refused rather than warned about: names
// repeat across clusters, so resolving it succeeds and acts on a *different*
// resource that happens to share a name.
type ContextMismatchError struct {
	Index   int
	Listed  string
	Current string
	// Relist names the command that rebuilds what this index counted against,
	// and is set only when kx cannot rebuild it itself.
	//
	// A history entry carries the `kx get` query that produced it, so the caller
	// replays that and shows a fresh listing; there is nothing to advise. A slot
	// carries no query, and the history stack's query relists something else
	// entirely — replaying it for `kx ns 2` answers with a pods table. So the
	// slot's error names its own relist command and is reported as-is.
	Relist string
}

func (e ContextMismatchError) Error() string {
	message := fmt.Sprintf(
		"Index %d was listed in context '%s'; the current context is '%s'.",
		e.Index, e.Listed, e.Current)
	if e.Relist == "" {
		return message
	}
	return message + fmt.Sprintf(" Run '%s' to relist here.", e.Relist)
}

// ErrSchemaChanged is returned when a state file's version doesn't match this
// build's — including files with no version key at all, which is every file
// written before versioning existed. The file has already been reset to a
// fresh, empty, current-version History by the time this is returned: kx
// state is ephemeral, and re-deriving it via `kx get` is cheaper and more
// honest than migrating old files or asking the user to delete one by hand.
var ErrSchemaChanged = errors.New(
	"kx's saved state format has changed since this file was written. " +
		"State has been reset (this does not affect your cluster) — run " +
		"`kx get <resource>` to rebuild it.")

// Service reads and writes the history stack at ~/.kx/state.json.
type Service struct {
	// MaxHistory caps retained entries. Always at least 1: a smaller value
	// would make the trim below a no-op and grow the file without bound.
	MaxHistory int
	// Path is the state file. Empty means ~/.kx/state.json.
	Path string
	// Context reports the active kubeconfig context, stamped onto every entry
	// this service writes.
	//
	// A hook rather than a value so it is read at save time, not at service
	// construction: `kx context 2` switches contexts inside a single process,
	// and the listing saved after that switch belongs to the new one.
	//
	// Nil leaves entries unstamped, which reads as "unknown" everywhere it is
	// consumed — the shape a Service built literally in a test has.
	Context func() string
}

// context reports the active context, or "" when no hook is wired.
func (s *Service) context() string {
	if s.Context == nil {
		return ""
	}
	return s.Context()
}

// stamp records the context an entry was listed against, leaving one the
// caller already set alone.
func (s *Service) stamp(entry State) State {
	if entry.Context == "" {
		entry.Context = s.context()
	}
	return entry
}

// NewService builds a state service with the configured history depth.
func NewService(maxHistory int) *Service {
	if maxHistory < 1 {
		maxHistory = 1
	}
	return &Service{MaxHistory: maxHistory}
}

// File returns the default state file path, mirroring config.File so the help
// screen can name both without hardcoding either.
func File() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate home directory: %w", err)
	}
	return filepath.Join(home, ".kx", "state.json"), nil
}

func (s *Service) path() (string, error) {
	if s.Path != "" {
		return s.Path, nil
	}
	return File()
}

func (s *Service) loadHistory() (History, error) {
	path, err := s.path()
	if err != nil {
		return History{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return History{}, ErrNoState
	}
	if err != nil {
		return History{}, ErrNoState
	}

	// Corrupt or foreign JSON (partial write, hand-edit, old schema). A
	// `kx get` rebuilds state, so this is recoverable, not fatal.
	unreadable := func(cause error) error {
		return fmt.Errorf(
			"State file at %s is unreadable (%v). Run `kx get <resource>` to rebuild it.",
			path, cause,
		)
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return History{}, unreadable(err)
	}

	// A missing or mismatched version covers both a foreign/future schema and
	// every file written before versioning existed (including the pre-history
	// single-entry shape this branch used to special-case) — all of them are
	// treated the same way: reset now, rather than try to keep reading an
	// on-disk shape this build no longer promises to understand.
	var version int
	if raw, ok := probe["version"]; !ok || json.Unmarshal(raw, &version) != nil || version != currentSchemaVersion {
		if err := s.saveHistory(History{States: []State{}, Cursor: 0}); err != nil {
			return History{}, err
		}
		return History{}, ErrSchemaChanged
	}

	if _, ok := probe["states"]; !ok {
		return History{}, unreadable(errors.New("'states'"))
	}

	if _, ok := probe["cursor"]; !ok {
		return History{}, unreadable(errors.New("'cursor'"))
	}
	// Unmarshal entry by entry so a missing "resources" key is rejected rather
	// than silently becoming an empty listing — Go's zero values would accept
	// what Python's dict access raises KeyError on, and an empty entry makes
	// every index unresolvable with no explanation.
	var raw struct {
		States []map[string]json.RawMessage `json:"states"`
		Cursor int                          `json:"cursor"`
		// Absent in files written before slots existed, which read as nil.
		// Decoded per entry like the stack, so a slot cannot smuggle in the
		// empty listing the loop below exists to reject.
		Named map[kinds.Kind]map[string]json.RawMessage `json:"named"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return History{}, unreadable(err)
	}

	history := History{Cursor: raw.Cursor, States: make([]State, 0, len(raw.States))}
	for _, entry := range raw.States {
		state, err := decodeEntry(entry)
		if err != nil {
			return History{}, unreadable(err)
		}
		// Only the stack gets this. A slot records the scope it was listed in,
		// and a context slot's scope is a context, which is legitimately empty
		// when the kubeconfig has no current one — defaulting it to "default"
		// would caption the listing with a context that does not exist.
		//
		// Nor does a listing that spans namespaces: its resources each record
		// their own, so an empty entry namespace is the accurate answer rather
		// than an unrecorded one, and "default" would caption an all-namespace
		// listing with a namespace it never came from.
		if state.Namespace == "" && !state.Resources.Spanning() {
			state.Namespace = "default"
		}
		history.States = append(history.States, state)
	}
	for kind, entry := range raw.Named {
		state, err := decodeEntry(entry)
		if err != nil {
			// Drop the slot rather than condemn the file, which is where this
			// parts company with the stack above. The recoveries differ: a bad
			// stack entry is answered by the `kx get <resource>` the unreadable
			// error names, but no `kx get` refills a slot, and an absent one
			// already reports itself as "run 'kx ns'" — the right instruction
			// for the thing that is actually broken. The history is unrelated
			// and survives.
			continue
		}
		if history.Named == nil {
			history.Named = make(map[kinds.Kind]State, len(raw.Named))
		}
		history.Named[kind] = state
	}
	if len(history.States) == 0 {
		// No stack entries: either a fresh reset file, or slots-only (`kx ns`
		// on a fresh install writes a slot before any `kx get` has pushed an
		// entry). Either way the file is well-formed; it just has nothing for
		// the stack readers yet, which Load reports as ErrNoState rather than
		// as corruption.
		history.Cursor = 0
		return history, nil
	}
	if history.Cursor < 0 || history.Cursor >= len(history.States) {
		return History{}, unreadable(errors.New("'cursor' out of range"))
	}
	return history, nil
}

// decodeEntry turns one raw state object into a State.
//
// A missing "resources" key is rejected rather than silently becoming an empty
// listing — Go's zero values accept what Python's dict access raised KeyError
// on, and an empty entry makes every index unresolvable with no explanation.
// Callers decide what an undecodable entry costs; see loadHistory.
func decodeEntry(entry map[string]json.RawMessage) (State, error) {
	if _, ok := entry["resources"]; !ok {
		return State{}, errors.New("'resources'")
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(encoded, &state); err != nil {
		return State{}, err
	}
	return state, nil
}

// saveHistory writes the stack via a sibling temp file and an atomic rename, so
// an interrupted write can never leave a truncated state.json.
func (s *Service) saveHistory(history History) error {
	path, err := s.path()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if history.States == nil {
		history.States = []State{}
	}
	history.Version = currentSchemaVersion
	data, err := json.Marshal(history)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// Save pushes a new entry, truncating any entries ahead of the cursor and
// trimming the stack to MaxHistory.
//
// A listing of a slotted kind also refreshes that kind's slot, so `kx get ns`
// serves both readers: the stack, for `kx describe <n>` on a namespace, and the
// slot, for `kx ns <n>`. Only `kx ns` itself skips the stack, via SaveNamed.
func (s *Service) Save(state State) error {
	maxHistory := s.MaxHistory
	if maxHistory < 1 {
		maxHistory = 1
	}
	state = s.stamp(state)

	var states []State
	var named map[kinds.Kind]State
	if history, err := s.loadHistory(); err == nil {
		// A slots-only file has no stack to truncate against.
		if len(history.States) > 0 {
			states = append(states, history.States[:history.Cursor+1]...)
		}
		named = history.Named
	}
	states = append(states, state)
	if len(states) > maxHistory {
		states = states[len(states)-maxHistory:]
	}
	if kind := soleKind(state); slottedKinds[kind] {
		if named == nil {
			named = map[kinds.Kind]State{}
		}
		named[kind] = state
	}
	return s.saveHistory(History{States: states, Cursor: len(states) - 1, Named: named})
}

// Load returns the entry at the cursor.
func (s *Service) Load() (State, error) {
	history, err := s.loadHistory()
	if err != nil {
		return State{}, err
	}
	if len(history.States) == 0 {
		return State{}, ErrNoState
	}
	return history.States[history.Cursor], nil
}

// LoadHistory returns the whole stack.
func (s *Service) LoadHistory() (History, error) { return s.loadHistory() }

func clamp(value, high int) int {
	if value < 0 {
		return 0
	}
	if value > high {
		return high
	}
	return value
}

// Navigate moves the cursor by delta, clamped to the stack.
func (s *Service) Navigate(delta int) (State, error) {
	history, err := s.loadHistory()
	if err != nil {
		return State{}, err
	}
	if len(history.States) == 0 {
		return State{}, ErrNoState
	}
	history.Cursor = clamp(history.Cursor+delta, len(history.States)-1)
	if err := s.saveHistory(history); err != nil {
		return State{}, err
	}
	return history.States[history.Cursor], nil
}

// NavigateTo moves the cursor to a 1-based position, clamped to the stack.
func (s *Service) NavigateTo(position int) (State, error) {
	history, err := s.loadHistory()
	if err != nil {
		return State{}, err
	}
	if len(history.States) == 0 {
		return State{}, ErrNoState
	}
	history.Cursor = clamp(position-1, len(history.States)-1)
	if err := s.saveHistory(history); err != nil {
		return State{}, err
	}
	return history.States[history.Cursor], nil
}

// Drop removes the entry at a 1-based position, keeping the cursor pointing at
// the same entry where possible.
func (s *Service) Drop(position int) (History, error) {
	history, err := s.loadHistory()
	if err != nil {
		return History{}, err
	}
	if len(history.States) == 0 {
		return History{}, ErrNoState
	}
	if len(history.States) == 1 {
		return History{}, errors.New("Cannot drop the only state entry.")
	}
	i := clamp(position-1, len(history.States)-1)
	history.States = append(history.States[:i], history.States[i+1:]...)
	if i < history.Cursor {
		history.Cursor--
	} else {
		history.Cursor = clamp(history.Cursor, len(history.States)-1)
	}
	if err := s.saveHistory(history); err != nil {
		return History{}, err
	}
	return history, nil
}

// DropAll clears the entire on-disk state — the navigation stack and the
// namespace/context slots together — resetting to what a fresh install has.
// Unlike Drop, which refuses to remove the last remaining entry, DropAll has
// no such guard: clearing everything is the point.
func (s *Service) DropAll() error {
	return s.saveHistory(History{})
}

// namespaceAt reports the namespace the resource at a 1-based index lives in.
//
// The resource's own namespace wins, falling back to the entry's. Both shapes
// are legitimate and neither can be dropped: a listing that spans namespaces
// (`kx get -A`) has no single entry namespace to fall back to, and an ordinary
// single-namespace listing records nothing per resource, so a lookup that only
// consulted the resource would resolve every index to the empty namespace.
func namespaceAt(entry State, idx int) string {
	if resource, ok := entry.Resources.At(idx); ok && resource.Namespace != "" {
		return resource.Namespace
	}
	return entry.Namespace
}

// checkContext refuses an index counted against a listing from another cluster.
//
// Either side unknown waives the check rather than failing it: a kubeconfig
// with no current-context is a legitimate setup, and treating "I don't know"
// as "they differ" would break kx for it entirely.
//
// Checked before the index is resolved, so a listing from the wrong cluster is
// reported as such even when the index is also out of range — the alternative
// answers with the size of a listing the user is no longer looking at.
// relist names the command that rebuilds what the index counted against, and is
// empty for a history entry, which the caller replays from its own saved query.
func (s *Service) checkContext(entry State, idx int, relist string) error {
	current := s.context()
	if entry.Context == "" || current == "" || entry.Context == current {
		return nil
	}
	return ContextMismatchError{
		Index: idx, Listed: entry.Context, Current: current, Relist: relist,
	}
}

// Fields resolves an index to the resource it names, plus its namespace and kind.
func (s *Service) Fields(idx int) (name, namespace string, kind kinds.Kind, err error) {
	current, err := s.Load()
	if err != nil {
		return "", "", "", err
	}
	if err := s.checkContext(current, idx, ""); err != nil {
		return "", "", "", err
	}
	name, err = index.Resolve(current, idx)
	if err != nil {
		return "", "", "", err
	}
	if entry, ok := current.Resources.At(idx); ok {
		kind = entry.Kind
	}
	return name, namespaceAt(current, idx), kind, nil
}

// Count returns how many resources are in the current listing — the same
// entry Fields resolves indexes against. Used to resolve the open end of a
// "5.." range.
func (s *Service) Count() (int, error) {
	current, err := s.Load()
	if err != nil {
		return 0, err
	}
	return current.Resources.Len(), nil
}

// soleKind returns the kind every resource in an entry shares, or "" when the
// entry spans several — a namespace-wide tree, a triage sweep.
func soleKind(entry State) kinds.Kind {
	entries := entry.Resources.Entries()
	if len(entries) == 0 {
		return ""
	}
	first := entries[0].Kind
	for _, e := range entries[1:] {
		if e.Kind != first {
			return ""
		}
	}
	return first
}

// describeCurrent names the listing an index was counted against — "1 Service",
// "14 Pods", or "3 items" when the entry spans kinds — so an out-of-range error
// can say what it was counting.
func describeCurrent(entry State) string {
	count := entry.Resources.Len()
	kind := soleKind(entry)
	if kind == "" {
		if count == 1 {
			return "1 item"
		}
		return fmt.Sprintf("%d items", count)
	}
	if count == 1 {
		return "1 " + string(kind)
	}
	return fmt.Sprintf("%d %s", count, kinds.PluralDisplay(string(kind)))
}

// FieldsExpecting resolves an index for a command that has already named the
// kind it wants.
//
// Fields resolves the index first and checks the kind afterwards, so the two
// failures that happen before the check — an index past the end of the current
// listing, and no state at all — were reported without reference to the kind
// asked for. `kx ns 2` against a one-row Services listing said "current state
// has 1 item (run 'kx state' to view)", which describes a different resource
// and points somewhere unhelpful. Every failure here names the kind instead.
func (s *Service) FieldsExpecting(
	idx int, expected kinds.Kind,
) (name, namespace string, err error) {
	// The relist command is spelled the way EnsureKind spells it, so all three
	// failures point at the same place.
	relist := "kx get " + strings.ToLower(string(expected))
	plural := kinds.PluralDisplay(string(expected))

	current, err := s.Load()
	if err != nil {
		if errors.Is(err, ErrNoState) {
			return "", "", fmt.Errorf(
				"No state found — run '%s' to list %s first.", relist, plural)
		}
		// An unreadable state file already explains itself.
		return "", "", err
	}

	if err := s.checkContext(current, idx, ""); err != nil {
		return "", "", err
	}

	name, err = index.Resolve(current, idx)
	if err != nil {
		return "", "", fmt.Errorf(
			"Index %d is out of range — the current listing has %s. Run '%s' to relist %s%s.",
			idx, describeCurrent(current), relist, plural, s.backHint(expected))
	}

	var kind kinds.Kind
	if entry, ok := current.Resources.At(idx); ok {
		kind = entry.Kind
	}
	if err := kinds.EnsureKind(idx, name, kind, expected, s); err != nil {
		return "", "", err
	}
	return name, namespaceAt(current, idx), nil
}

// backHint offers `kx back` when the entry one step back lists the kind asked
// for, matching the clause EnsureKind appends.
func (s *Service) backHint(expected kinds.Kind) string {
	if !s.PreviousLists(expected) {
		return ""
	}
	return fmt.Sprintf(", or 'kx back' for the previous %s listing", expected)
}

// PreviousLists reports whether the entry one step back lists kind, so
// `kx back` would reach it.
//
// Best-effort: this only decorates an error message, so an unreadable history
// yields no hint rather than displacing the real error.
func (s *Service) PreviousLists(kind kinds.Kind) bool {
	history, err := s.loadHistory()
	if err != nil {
		return false
	}
	previous := history.Cursor - 1
	if previous < 0 || previous >= len(history.States) {
		return false
	}
	for _, e := range history.States[previous].Resources.Entries() {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

// slottedKinds are the kinds kept in a slot as well as, or instead of, the
// history stack.
//
// Both are switch targets rather than things you act on by index, and both are
// listed often enough that stacking every listing crowds out the work the stack
// exists for. Nothing else earns a slot: a slot only pays off for a listing
// whose indexes are consumed by a command that already names the kind.
var slottedKinds = map[kinds.Kind]bool{
	kinds.Namespace: true,
	kinds.Context:   true,
}

// listCommandFor names the command that relists kind, for the hints below.
// Namespaces and contexts have a shorter spelling than `kx get <kind>`, and it
// is the one their errors should teach.
func listCommandFor(kind kinds.Kind) string {
	switch kind {
	case kinds.Namespace:
		return "kx ns"
	case kinds.Context:
		return "kx contexts"
	default:
		return "kx get " + strings.ToLower(string(kind))
	}
}

// SaveNamed records a listing in its per-kind slot without pushing it onto the
// history stack.
//
// This is the `kx ns` path. Keeping it out of the stack is the point: switching
// namespaces is the most frequent thing kx does, and every switch used to cost
// a history entry, so `kx back` walked through namespace listings instead of
// the work between them.
func (s *Service) SaveNamed(entry State) error {
	kind := soleKind(entry)
	if kind == "" {
		return fmt.Errorf("state: a slot needs a single-kind listing")
	}
	entry = s.stamp(entry)
	history, err := s.loadHistory()
	if err != nil {
		// No usable stack yet. The slot is independent of it, so it is still
		// worth writing — `kx ns` on a fresh install must leave something for
		// `kx ns 2` to resolve against.
		history = History{States: []State{}, Cursor: 0}
	}
	if history.Named == nil {
		history.Named = map[kinds.Kind]State{}
	}
	history.Named[kind] = entry
	return s.saveHistory(history)
}

// FieldsNamed resolves an index against the slot for kind.
//
// The slot, not the history cursor: `kx ns 2` has already said which kind it
// means, so an intervening `kx get pods` must not turn that 2 into a pod, which
// is what made the sequence in #156 fail. There is no fallback to the current
// entry — that fallback *is* the bug.
func (s *Service) FieldsNamed(idx int, kind kinds.Kind) (name, namespace string, err error) {
	relist := listCommandFor(kind)
	plural := kinds.PluralDisplay(string(kind))

	history, err := s.loadHistory()
	if err != nil && !errors.Is(err, ErrNoState) {
		// An unreadable state file already explains itself.
		return "", "", err
	}
	entry, ok := history.Named[kind]
	if !ok {
		return "", "", fmt.Errorf(
			"No %s listing yet — run '%s' to list them.", plural, relist)
	}

	// Contexts are the one listing that isn't cluster-bound: they live in
	// kubeconfig, and this slot exists to switch between them. Guarding it would
	// make `kx context 2` unusable the moment it had been used once, because the
	// switch it performs is itself what creates the mismatch — you could switch
	// away and never switch back. Every other slot is a server object and is
	// checked; see checkContext.
	if kind != kinds.Context {
		if err := s.checkContext(entry, idx, relist); err != nil {
			return "", "", err
		}
	}

	name, err = index.Resolve(entry, idx)
	if err != nil {
		// index.Resolve says "current state", which would be wrong here: the
		// count comes from the slot, not from whatever the cursor is on, and
		// relisting is what fixes it.
		return "", "", fmt.Errorf(
			"Index %d is out of range — the last listing had %s. Run '%s' to relist.",
			idx, describeCurrent(entry), relist)
	}
	return name, namespaceAt(entry, idx), nil
}

// compile-time checks that the service satisfies the interfaces its consumers
// declare.
var (
	_ kinds.PreviousLister = (*Service)(nil)
	_ index.Resolver       = State{}
)
