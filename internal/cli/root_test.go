package cli

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/config"
)

// Every other test in this package builds Services literally, so nothing else
// would notice if the production wiring stopped stamping the kubeconfig context
// onto saved state: the field would simply be empty for every real user, and an
// empty context is the value that waives the cluster check entirely.
func TestNewServicesWiresTheStateContextHook(t *testing.T) {
	services := NewServices(config.Config{MaxHistory: 10})

	if services.State.Context == nil {
		t.Fatal("NewServices left State.Context nil — saved state would record no context")
	}
}

// kx completion's examples are per-shell alternatives, and the help screen
// renders each on its own "$ " line with nothing between them. Unlabelled,
// they read as a two-step sequence — and someone on zsh ran both:
//
//	kx completion zsh > "${fpath[1]}/_kx"
//	source <(kx completion bash)
//
// The second line defines __start_kx and, where bashcompinit is loaded,
// registers it for kx with `complete -F`, shadowing the native _kx installed
// by the first. Every Tab then ran cobra's bash completer, whose
// `declare -F _init_completion` guard means float formatting in zsh rather
// than "is this a function" — so it passed, called a bash-completion helper
// zsh does not have, and printed "command not found: _init_completion".
//
// Nothing in kx can stop a bash script being sourced into zsh. What kx
// controls is whether its own examples invite it.
func TestCompletionExampleLabelsEachShell(t *testing.T) {
	root := NewRoot(Services{Config: config.Config{}}, "test")

	completion, _, err := root.Find([]string{"completion"})
	if err != nil {
		t.Fatalf("Find(completion): %v", err)
	}

	lines := strings.Split(strings.TrimSpace(completion.Example), "\n")
	if len(lines) < 2 {
		t.Fatalf("completion.Example has %d lines, want one per shell:\n%s",
			len(lines), completion.Example)
	}
	for _, line := range lines {
		if !strings.Contains(line, "#") {
			t.Errorf("example line names no shell, so it reads as part of a "+
				"sequence rather than an alternative:\n  %s", line)
		}
	}

	// Each line has to say which shell it is for by name, not just carry some
	// comment: "# then start a new shell" would satisfy the check above while
	// leaving the two lines exactly as confusable as before.
	for shell, want := range map[string]string{"zsh": "zsh", "bash": "bash"} {
		found := false
		for _, line := range lines {
			if comment := line[strings.Index(line, "#")+1:]; strings.Contains(comment, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no example comment names %s:\n%s", shell, completion.Example)
		}
	}
}
