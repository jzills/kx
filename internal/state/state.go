// Package state persists the resource listing produced by `kx get` as a
// history stack, and resolves indexes against the current entry.
package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
)

// Resource is one indexed row: the name kubectl reported and the kind it was
// listed as.
type Resource struct {
	Name string
	Kind kinds.Kind
}

// Resources is an ordered name→kind mapping.
//
// Order is load-bearing: an index resolves to the nth entry, so this must
// preserve the order names were listed in and the order they appear in the
// JSON object on disk. That rules out a Go map, whose iteration order is
// randomized — a map here would resolve indexes to different resources on
// different runs. The JSON representation is a plain object, so a state file
// written by any version of kx keeps resolving indexes the same way.
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

// Kind returns the kind recorded for name.
func (r Resources) Kind(name string) (kinds.Kind, bool) {
	for _, e := range r.entries {
		if e.Name == name {
			return e.Kind, true
		}
	}
	return "", false
}

// MarshalJSON writes a JSON object with keys in index order.
func (r Resources) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, e := range r.entries {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(e.Name)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(string(e.Kind))
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// UnmarshalJSON reads a JSON object, preserving key order via the token
// stream — encoding/json into a map would discard it.
func (r *Resources) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("resources: expected a JSON object")
	}

	r.entries = nil
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("resources: expected a string key")
		}
		var kind string
		if err := decoder.Decode(&kind); err != nil {
			return err
		}
		r.entries = append(r.entries, Resource{Name: name, Kind: kinds.Kind(kind)})
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	return nil
}

// Query is the `kx get` invocation that produced a state entry, kept so a
// stale entry can be re-run. Nil for entries not created by `kx get`
// (tree --index, pre-existing state files).
type Query struct {
	Resource string   `json:"resource"`
	Args     []string `json:"args"`
	Match    *string  `json:"match"`
}

// State is one history entry: an indexed listing and the namespace it came from.
type State struct {
	Resources Resources `json:"resources"`
	Namespace string    `json:"namespace"`
	Query     *Query    `json:"query"`
}

// Names satisfies index.Resolver.
func (s State) Names() []string { return s.Resources.Names() }

// History is the stack of listings with a cursor marking the current entry.
type History struct {
	States []State `json:"states"`
	Cursor int     `json:"cursor"`
}

// ErrNoState is returned when no state file exists yet.
var ErrNoState = errors.New("No state found. Run `kx get <resource>` first.")

// Service reads and writes the history stack at ~/.kx/state.json.
type Service struct {
	// MaxHistory caps retained entries. Always at least 1: a smaller value
	// would make the trim below a no-op and grow the file without bound.
	MaxHistory int
	// Path is the state file. Empty means ~/.kx/state.json.
	Path string
}

// NewService builds a state service with the configured history depth.
func NewService(maxHistory int) *Service {
	if maxHistory < 1 {
		maxHistory = 1
	}
	return &Service{MaxHistory: maxHistory}
}

func (s *Service) path() (string, error) {
	if s.Path != "" {
		return s.Path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate home directory: %w", err)
	}
	return filepath.Join(home, ".kx", "state.json"), nil
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
	if _, ok := probe["states"]; !ok {
		// Legacy single-entry file written before history existed.
		var single State
		if err := json.Unmarshal(data, &single); err != nil {
			return History{}, unreadable(err)
		}
		if single.Resources.entries == nil {
			return History{}, unreadable(errors.New("'resources'"))
		}
		if single.Namespace == "" {
			single.Namespace = "default"
		}
		return History{States: []State{single}, Cursor: 0}, nil
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
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return History{}, unreadable(err)
	}

	history := History{Cursor: raw.Cursor, States: make([]State, 0, len(raw.States))}
	for _, entry := range raw.States {
		if _, ok := entry["resources"]; !ok {
			return History{}, unreadable(errors.New("'resources'"))
		}
		encoded, err := json.Marshal(entry)
		if err != nil {
			return History{}, unreadable(err)
		}
		var state State
		if err := json.Unmarshal(encoded, &state); err != nil {
			return History{}, unreadable(err)
		}
		if state.Namespace == "" {
			state.Namespace = "default"
		}
		history.States = append(history.States, state)
	}
	if len(history.States) == 0 {
		return History{}, unreadable(errors.New("no state entries"))
	}
	if history.Cursor < 0 || history.Cursor >= len(history.States) {
		return History{}, unreadable(errors.New("'cursor' out of range"))
	}
	return history, nil
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
func (s *Service) Save(state State) error {
	maxHistory := s.MaxHistory
	if maxHistory < 1 {
		maxHistory = 1
	}

	var states []State
	if history, err := s.loadHistory(); err == nil {
		states = append(states, history.States[:history.Cursor+1]...)
	}
	states = append(states, state)
	if len(states) > maxHistory {
		states = states[len(states)-maxHistory:]
	}
	return s.saveHistory(History{States: states, Cursor: len(states) - 1})
}

// Load returns the entry at the cursor.
func (s *Service) Load() (State, error) {
	history, err := s.loadHistory()
	if err != nil {
		return State{}, err
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

// Fields resolves an index to the resource it names, plus its namespace and kind.
func (s *Service) Fields(idx int) (name, namespace string, kind kinds.Kind, err error) {
	current, err := s.Load()
	if err != nil {
		return "", "", "", err
	}
	name, err = index.Resolve(current, idx)
	if err != nil {
		return "", "", "", err
	}
	kind, _ = current.Resources.Kind(name)
	return name, current.Namespace, kind, nil
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

// compile-time checks that the service satisfies the interfaces its consumers
// declare.
var (
	_ kinds.PreviousLister = (*Service)(nil)
	_ index.Resolver       = State{}
)
