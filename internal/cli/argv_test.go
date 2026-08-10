package cli

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
)

// Argument handling is exercised here through cli.Execute rather than against
// the command structs, because the commands that forward flags to kubectl split
// argv by hand (see passthrough.go) and that split only runs inside RunE. A test
// that calls the helpers directly agrees with itself; only real argv proves the
// wiring.

// argvServices builds a service set backed by a temp state file and a kubectl
// that always fails, so a command that reaches the cluster reports an error
// rather than hanging or spawning a process.
func argvServices(t *testing.T) Services {
	t.Helper()
	return Services{
		Kubectl: &fakeKubectl{err: errors.New("kubectl unavailable in tests")},
		State:   &state.Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")},
		Index:   index.Service{},
		Config:  config.Default(),
		Kubernetes: func() (kubernetes.Interface, error) {
			return nil, errors.New("no cluster in tests")
		},
	}
}

// captureRender points the package renderer at one buffer for both streams, so
// command output stays out of the test log and the order lines were written in
// is preserved across stdout and stderr.
func captureRender(t *testing.T) *bytes.Buffer {
	t.Helper()
	var sink bytes.Buffer
	render.SetOutput(&sink, &sink, config.DefaultTheme)
	t.Cleanup(func() { render.SetOutput(nil, nil, config.DefaultTheme) })
	return &sink
}

func quietRender(t *testing.T) { t.Helper(); captureRender(t) }

// A command that sets DisableFlagParsing strips its own flags out of argv by
// hand, so cobra only knows a flag exists if it is also registered. An
// unregistered flag keeps working and simply vanishes from --help, which is a
// failure nothing else notices.
func TestHandParsedFlagsAppearInHelp(t *testing.T) {
	want := map[string][]string{
		"get":    {"--match", "--decode", "--key", "--yes"},
		"secret": {"--match", "--decode", "--key", "--yes"},
		"scan": {
			"--engine", "--full", "--namespace", "--all-namespaces",
			"--html", "--port", "--no-open",
		},
		"top": {"--match", "--no-limits"},
	}
	byName := map[string]*cobra.Command{}
	for _, cmd := range NewRoot(argvServices(t), "test").Commands() {
		byName[cmd.Name()] = cmd
	}

	for name, flags := range want {
		cmd, ok := byName[name]
		if !ok {
			t.Errorf("no %q command on the root", name)
			continue
		}
		var documented []string
		for _, option := range commandHelp(cmd).Options {
			documented = append(documented, option.Name)
		}
		joined := strings.Join(documented, " ")
		for _, flag := range flags {
			if !strings.Contains(joined, flag) {
				t.Errorf("kx %s --help omits %s; options are %q", name, flag, joined)
			}
		}
	}
}

// A command whose argv is nothing but kx's own flags has an empty argument list
// by the time it looks for an index. Cobra's arity check has already passed on
// the unstripped argv, so nothing stands between that empty slice and the index
// expression that reads it — the failure mode is a panic, not an error.
func TestArgvOfOnlyStrippedFlagsIsAnErrorNotAPanic(t *testing.T) {
	cases := []struct {
		name string
		argv []string
	}{
		{"get", []string{"get", "--no-color"}},
		{"describe", []string{"describe", "--no-color"}},
		{"logs", []string{"logs", "--no-color"}},
		{"edit", []string{"edit", "--no-color"}},
		{"exec", []string{"exec", "--no-color"}},
		// port-forward takes two arguments, so two stripped flags are needed to
		// get past the arity check and reach the index lookup.
		{"port-forward", []string{"port-forward", "--no-color", "--no-color"}},
		{"secret", []string{"secret", "--no-color"}},
		{"top", []string{"top", "--no-color"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			quietRender(t)
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("kx %v panicked: %v", tc.argv, r)
				}
			}()
			if err := Execute(NewRoot(argvServices(t), "test"), tc.argv); err == nil {
				t.Errorf("kx %v returned no error", tc.argv)
			}
		})
	}
}

// The pre-restructure spellings (kx back/forward) must keep working — they're
// hidden from --help, not removed — so scripts and muscle memory written
// before kx state back/forward/drop existed don't break.
func TestLegacyTopLevelHistoryCommandsStillWork(t *testing.T) {
	services := argvServices(t)
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"one"}, kinds.Pod), Namespace: "default",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"two"}, kinds.Pod), Namespace: "default",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	for _, name := range []string{"back", "forward"} {
		quietRender(t)
		if err := Execute(NewRoot(services, "test"), []string{name}); err != nil {
			t.Errorf("kx %s: %v", name, err)
		}
	}
}

// The canonical spellings resolve to the same subcommands nested under state.
//
// cobra's Find falls back to the deepest command it can match rather than
// erroring on an unrecognized trailing argument, so root.Find([]string{"state",
// "back"}) returns "state" with no error even when "back" isn't a real
// subcommand — the resolved command's own Name() must be checked, not just
// the error, or this test cannot fail.
func TestStateSubcommandsAreRegistered(t *testing.T) {
	root := NewRoot(argvServices(t), "test")
	stateCmd, _, err := root.Find([]string{"state"})
	if err != nil {
		t.Fatalf("root.Find(state): %v", err)
	}
	for _, name := range []string{"back", "drop", "forward"} {
		cmd, _, err := root.Find([]string{"state", name})
		if err != nil {
			t.Errorf("kx state %s is not registered: %v", name, err)
			continue
		}
		if cmd.Name() != name {
			t.Errorf("root.Find(state, %s) resolved to %q, want %q", name, cmd.Name(), name)
		}
	}
	for _, child := range stateCmd.Commands() {
		if child.Hidden {
			t.Errorf("kx state %s is hidden, want it visible", child.Name())
		}
	}
}

// The legacy top-level spellings are hidden from --help, not removed.
func TestLegacyTopLevelHistoryCommandsAreHidden(t *testing.T) {
	root := NewRoot(argvServices(t), "test")
	for _, name := range []string{"back", "forward", "drop"} {
		cmd, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatalf("root.Find(%s): %v", name, err)
		}
		if !cmd.Hidden {
			t.Errorf("kx %s is not hidden, want it hidden (kx state %s is now canonical)", name, name)
		}
	}
}
