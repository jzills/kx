package kubectlflags

import (
	"regexp"
	"slices"
	"testing"
)

func find(t *testing.T, flags []Flag, name string) Flag {
	t.Helper()
	i := slices.IndexFunc(flags, func(f Flag) bool { return f.Name == name })
	if i < 0 {
		t.Fatalf("no --%s in the table", name)
	}
	return flags[i]
}

// The pair kx cannot tell apart without the table: in `kx get pods -A 1` the 1
// is an index, in `kx get pods -n 5` the 5 is a namespace.
func TestTableKnowsWhichFlagsTakeAValue(t *testing.T) {
	get, ok := Command("get")
	if !ok {
		t.Fatal("no get in the table")
	}
	if a := find(t, get, "all-namespaces"); a.Shorthand != "A" || a.TakesValue() {
		t.Errorf("--all-namespaces = %+v, want -A taking no value", a)
	}
	if c := find(t, get, "chunk-size"); !c.TakesValue() {
		t.Errorf("--chunk-size = %+v, want it to take a value", c)
	}
	// A string flag with a default for its bare form: --dry-run alone means
	// "unchanged", so it never consumes the next argument either.
	if d := find(t, mustCommand(t, "delete"), "dry-run"); d.Type != "string" || d.TakesValue() {
		t.Errorf("--dry-run = %+v, want a string flag taking no separate value", d)
	}
	if n := find(t, Global(), "namespace"); n.Shorthand != "n" || !n.TakesValue() {
		t.Errorf("--namespace = %+v, want -n taking a value", n)
	}
}

func mustCommand(t *testing.T, path string) []Flag {
	t.Helper()
	flags, ok := Command(path)
	if !ok {
		t.Fatalf("no %q in the table", path)
	}
	return flags
}

// A subcommand can define a flag under a global flag's name, and its own is
// the one kubectl applies: kubectl debug's --profile picks a debugging profile,
// where the global --profile names a pprof profile to capture.
func TestASubcommandFlagCanShadowAGlobalOne(t *testing.T) {
	debug := find(t, mustCommand(t, "debug"), "profile")
	global := find(t, Global(), "profile")
	if debug.Usage == global.Usage {
		t.Errorf("debug's --profile has the global's usage %q, want its own", debug.Usage)
	}
}

func TestCommandPathsIncludeNestedSubcommands(t *testing.T) {
	for _, path := range []string{"rollout undo", "set image", "top pod"} {
		mustCommand(t, path)
	}
	if _, ok := Command("rollout"); ok {
		t.Error(`Command("rollout") found a table for a parent kx never forwards to`)
	}
}

func TestVersionIsAKubectlRelease(t *testing.T) {
	if !regexp.MustCompile(`^v1\.\d+\.\d+$`).MatchString(Version) {
		t.Errorf("Version = %q, want v1.X.Y", Version)
	}
}

// Callers get their own copy, so one cannot edit the table under another.
func TestCommandReturnsACopy(t *testing.T) {
	first := mustCommand(t, "get")
	first[0].Name = "changed"
	if mustCommand(t, "get")[0].Name == "changed" {
		t.Error("editing Command's result changed the table")
	}
}
