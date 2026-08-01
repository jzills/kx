package cli

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/index"
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
		"scan":   {"--engine", "--full", "--namespace", "--all-namespaces"},
		"top":    {"--match", "--no-limits"},
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
