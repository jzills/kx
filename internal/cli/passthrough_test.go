package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

func joined(args []string) string { return strings.Join(args, " ") }

func TestExtractStringForms(t *testing.T) {
	cases := map[string]struct {
		args      []string
		wantValue string
		wantRest  string
	}{
		"long with space":    {[]string{"pods", "--match", "web"}, "web", "pods"},
		"long with equals":   {[]string{"pods", "--match=web"}, "web", "pods"},
		"short with space":   {[]string{"pods", "-m", "web"}, "web", "pods"},
		"short with equals":  {[]string{"pods", "-m=web"}, "web", "pods"},
		"absent":             {[]string{"pods", "-n", "prod"}, "", "pods -n prod"},
		"last wins":          {[]string{"pods", "-m", "a", "-m", "b"}, "b", "pods"},
		"attached shorthand": {[]string{"pods", "-mweb"}, "web", "pods"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			value, rest, err := extractString(tc.args, "--match", "-m")
			if err != nil {
				t.Fatalf("extractString: %v", err)
			}
			if value != tc.wantValue {
				t.Errorf("value = %q, want %q", value, tc.wantValue)
			}
			if joined(rest) != tc.wantRest {
				t.Errorf("rest = %q, want %q", joined(rest), tc.wantRest)
			}
		})
	}
}

// Regression test for the reported bug: `kx scan -ndiagnostics` swept the
// wrong namespace because the attached-shorthand spelling fell through to
// `rest` uncaught. Exercised with the real --namespace/-n pair rather than
// --match/-m so the fix is proven against the exact flags scan.go uses.
func TestExtractStringNamespaceAttachedShorthand(t *testing.T) {
	value, rest, err := extractString([]string{"-ndiagnostics"}, "--namespace", "-n")
	if err != nil {
		t.Fatalf("extractString: %v", err)
	}
	if value != "diagnostics" {
		t.Errorf("value = %q, want diagnostics", value)
	}
	if len(rest) != 0 {
		t.Errorf("rest = %q, want empty", joined(rest))
	}
}

// Regression guard on the len(arg) > len(short) condition: a bare "-n" must
// still take the following argument rather than being swallowed as an
// attached value trimmed down to "".
func TestExtractStringBareShortStillTakesNextArg(t *testing.T) {
	value, rest, err := extractString([]string{"-n", "diagnostics"}, "--namespace", "-n")
	if err != nil {
		t.Fatalf("extractString: %v", err)
	}
	if value != "diagnostics" {
		t.Errorf("value = %q, want diagnostics", value)
	}
	if len(rest) != 0 {
		t.Errorf("rest = %q, want empty", joined(rest))
	}
}

func TestExtractStringMissingValue(t *testing.T) {
	if _, _, err := extractString([]string{"pods", "-m"}, "--match", "-m"); err == nil {
		t.Error("extractString accepted a flag with no value")
	}
}

// The whole point of the helper: everything kx doesn't own reaches kubectl
// untouched, in its original order.
func TestExtractStringPreservesKubectlFlags(t *testing.T) {
	args := []string{"pods", "-n", "prod", "-l", "app=web", "--sort-by=.metadata.name", "-o", "wide"}
	value, rest, err := extractString(args, "--match", "-m")
	if err != nil {
		t.Fatalf("extractString: %v", err)
	}
	if value != "" {
		t.Errorf("value = %q, want empty", value)
	}
	if joined(rest) != joined(args) {
		t.Errorf("rest = %q, want it unchanged", joined(rest))
	}
}

func TestExtractStringKeepsKubectlFlagsAlongsideMatch(t *testing.T) {
	args := []string{"pods", "-n", "prod", "-m", "web", "-l", "app=api"}
	value, rest, err := extractString(args, "--match", "-m")
	if err != nil {
		t.Fatalf("extractString: %v", err)
	}
	if value != "web" {
		t.Errorf("value = %q, want web", value)
	}
	if want := "pods -n prod -l app=api"; joined(rest) != want {
		t.Errorf("rest = %q, want %q", joined(rest), want)
	}
}

// hasFlag must recognise exactly the spellings extractString consumes. If it
// falls behind — missing attached shorthand, say — a caller that checks
// presence before extracting (scan's --namespace/--all-namespaces guard) sees
// "absent" for a flag extractString is quietly consuming anyway.
func TestHasFlag(t *testing.T) {
	cases := map[string]struct {
		args []string
		want bool
	}{
		"long":               {[]string{"pods", "--namespace", "prod"}, true},
		"long with equals":   {[]string{"pods", "--namespace=prod"}, true},
		"short with space":   {[]string{"pods", "-n", "prod"}, true},
		"short with equals":  {[]string{"pods", "-n=prod"}, true},
		"attached shorthand": {[]string{"pods", "-nprod"}, true},
		"absent":             {[]string{"pods", "-l", "app=web"}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := hasFlag(tc.args, "--namespace", "-n"); got != tc.want {
				t.Errorf("hasFlag = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExtractBool(t *testing.T) {
	present, rest := extractBool([]string{"pods", "--no-color", "-n", "prod"}, "--no-color")
	if !present {
		t.Error("present = false, want true")
	}
	if want := "pods -n prod"; joined(rest) != want {
		t.Errorf("rest = %q, want %q", joined(rest), want)
	}

	if present, _ := extractBool([]string{"pods"}, "--no-color"); present {
		t.Error("present = true for absent flag")
	}
}

// "<name>=<value>" forms, covering both the long and short spellings of a
// flag with aliases (extractBool's variadic names). "=false" is the one case
// where a matched token must still be removed from rest without making the
// flag present.
func TestExtractBoolForms(t *testing.T) {
	cases := map[string]struct {
		args     []string
		want     bool
		wantRest string
	}{
		"bare long":         {[]string{"pods", "--all-namespaces", "-n", "prod"}, true, "pods -n prod"},
		"long equals true":  {[]string{"pods", "--all-namespaces=true", "-n", "prod"}, true, "pods -n prod"},
		"long equals false": {[]string{"pods", "--all-namespaces=false", "-n", "prod"}, false, "pods -n prod"},
		"short equals true": {[]string{"pods", "-A=true", "-n", "prod"}, true, "pods -n prod"},
		"unparseable value": {[]string{"pods", "--all-namespaces=banana"}, true, "pods"},
		"absent":            {[]string{"pods", "-n", "prod"}, false, "pods -n prod"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			present, rest := extractBool(tc.args, "--all-namespaces", "-A")
			if present != tc.want {
				t.Errorf("present = %v, want %v", present, tc.want)
			}
			if joined(rest) != tc.wantRest {
				t.Errorf("rest = %q, want %q", joined(rest), tc.wantRest)
			}
		})
	}
}

// A kubectl value that happens to equal a kx flag name is still consumed as
// kx's flag. Documenting the known limit: `--` style separation would be the
// fix if this ever bites, and it matches what the Python implementation does
// today, since Click also matches on token equality.
func TestExtractStringConsumesFlagLikeValue(t *testing.T) {
	value, rest, err := extractString([]string{"pods", "-m", "-n"}, "--match", "-m")
	if err != nil {
		t.Fatalf("extractString: %v", err)
	}
	if value != "-n" {
		t.Errorf("value = %q, want -n", value)
	}
	if joined(rest) != "pods" {
		t.Errorf("rest = %q, want pods", joined(rest))
	}
}

// One sentence, one spelling, wherever the rule is enforced — the sweep
// commands only add what they can offer instead.
func TestScopeFlagBesideIndexErrorQuotesTheSpellingTyped(t *testing.T) {
	err := scopeFlagBesideIndexError("-n", "")
	if err == nil {
		t.Fatal("scopeFlagBesideIndexError returned nil")
	}
	for _, want := range []string{
		"'-n' cannot be combined with an index",
		"already carries the namespace it was listed from",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q\n  missing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "sweep") {
		t.Errorf("err = %q, want no sweep hint where there is no sweep to offer", err)
	}
	swept := scopeFlagBesideIndexError("--namespace", sweepInsteadHint)
	if !strings.Contains(swept.Error(), "sweep the namespace instead") {
		t.Errorf("err = %q, want the sweep hint appended", swept)
	}
}

// A namespaced index already says where the resource is, so a scope flag next
// to one is a contradiction: kubectl takes the last -n, which for kx delete
// meant a prompt naming one namespace and a deletion in another.
func TestRefuseScopeFlagRejectsAScopeFlagBesideANamespacedIndex(t *testing.T) {
	for _, args := range [][]string{
		{"-n", "other"}, {"--namespace=other"}, {"-nother"}, {"-A"}, {"--all-namespaces"},
	} {
		if err := refuseScopeFlag(args, "diagnostics"); err == nil {
			t.Errorf("refuseScopeFlag(%v) = nil, want a refusal", args)
		}
	}
}

// A cluster-scoped index carries no namespace for -n to contradict. It is not
// always meaningless either: `kx debug <node-index>` creates a pod, and -n is
// where that pod lands — see DebugCommand.Execute.
func TestRefuseScopeFlagAllowsNamespaceForAClusterScopedIndex(t *testing.T) {
	if err := refuseScopeFlag([]string{"-n", "kube-system"}, ""); err != nil {
		t.Errorf("refuseScopeFlag = %v, want -n allowed for a cluster-scoped index", err)
	}
}

// -A is never a refinement of an index, whatever the index is scoped to:
// there is no listing here for it to widen.
func TestRefuseScopeFlagRejectsAllNamespacesEvenForAClusterScopedIndex(t *testing.T) {
	if err := refuseScopeFlag([]string{"-A"}, ""); err == nil {
		t.Error("refuseScopeFlag(-A) = nil for a cluster-scoped index, want a refusal")
	}
}

// Nothing to refuse leaves the args alone.
func TestRefuseScopeFlagPassesOtherFlags(t *testing.T) {
	if err := refuseScopeFlag([]string{"--force", "--grace-period=0"}, "prod"); err != nil {
		t.Errorf("refuseScopeFlag = %v, want kubectl's own flags untouched", err)
	}
}

// The namespaces are already on the Resolved values, so the guard must not
// ask the resolver for them again. PR 1 bridged with indexesOf() and paid a
// state load per index on a path that had the answer in hand.
func TestRefuseScopeFlagResolvedRefusesAScopeFlagBesideANamespacedReference(t *testing.T) {
	resolved := []Resolved{
		{Ref: state.Ref{Index: 1}, Name: "api", Namespace: "prod", Kind: kinds.Pod},
		{Ref: state.Ref{Index: 2}, Name: "web", Namespace: "prod", Kind: kinds.Pod},
	}

	if err := refuseScopeFlagResolved(resolved, []string{"-n", "other"}); err == nil {
		t.Error("a scope flag beside a namespaced reference was allowed")
	}
}

// A cluster-scoped reference carries no namespace for -n to contradict; -A is
// refused either way. Same rule refuseScopeFlag already applies.
func TestRefuseScopeFlagResolvedKeepsTheClusterScopedException(t *testing.T) {
	node := []Resolved{{Ref: state.Ref{Index: 1}, Name: "node-a", Kind: kinds.Node}}

	if err := refuseScopeFlagResolved(node, []string{"-n", "kube-system"}); err != nil {
		t.Errorf("refuseScopeFlagResolved = %v, want -n allowed for a cluster-scoped reference", err)
	}
	if err := refuseScopeFlagResolved(node, []string{"-A"}); err == nil {
		t.Error("-A beside a reference was allowed")
	}
}

// No references and a scope flag still refuses -A: there is no listing beside
// a reference for it to widen.
func TestRefuseScopeFlagResolvedWithNoReferencesStillRefusesAllNamespaces(t *testing.T) {
	if err := refuseScopeFlagResolved(nil, []string{"-A"}); err == nil {
		t.Error("-A with no references was allowed")
	}
}

// Every command that resolves an index and forwards flags refuses a scope
// flag, because kubectl takes the last -n and kx appends its own from the
// index — so a second one silently retargets the command. Enforced as one
// table rather than per command: the rule is the same everywhere, and a new
// pass-through command that forgets it should fail here.
func TestPassthroughCommandsRefuseAScopeFlagBesideAnIndex(t *testing.T) {
	commands := map[string]struct {
		build func(Services) *cobra.Command
		args  []string
	}{
		"describe":     {newDescribeCommand, []string{"1", "-n", "other"}},
		"logs":         {newLogsCommand, []string{"1", "-n", "other"}},
		"edit":         {newEditCommand, []string{"1", "-n", "other"}},
		"exec":         {newExecCommand, []string{"1", "-n", "other"}},
		"debug":        {newDebugCommand, []string{"1", "-n", "other"}},
		"port-forward": {newPortForwardCommand, []string{"1", "8080:80", "-n", "other"}},
		"cp":           {newCopyCommand, []string{"1:/tmp/x", "./y", "-n", "other"}},
		"drain":        {newDrainCommand, []string{"1", "-n", "other"}},
		"delete":       {newDeleteCommand, []string{"1", "-y", "-n", "other"}},
		"scale":        {newScaleCommand, []string{"1", "3", "-n", "other"}},
		"rollout":      {newRolloutCommand, []string{"status", "1", "-n", "other"}},
		"yaml":         {newYamlCommand, []string{"1", "-n", "other"}},
	}
	for name, spec := range commands {
		t.Run(name, func(t *testing.T) {
			kube := &recordingKubectl{output: "ok"}
			services := switchServices(t, kube)
			services.Confirm = func(string) error { return nil }
			if err := services.State.Save(state.State{
				Resources: state.NewResources([]string{"nginx"}, kinds.Pod),
				Namespace: "prod",
			}); err != nil {
				t.Fatalf("Save: %v", err)
			}
			cmd := spec.build(services)
			cmd.SetArgs(spec.args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("kx %s %v succeeded, want a refusal", name, spec.args)
			}
			if !strings.Contains(err.Error(), "cannot be combined with an index") {
				t.Errorf("err = %q, want the scope-flag refusal", err)
			}
			if calls := len(kube.runs) + len(kube.interactive); calls != 0 {
				t.Errorf("made %d kubectl calls for a refused command, want 0", calls)
			}
		})
	}
}

// The four commands that swallowed kubectl's flags now forward them. Asserted
// on the exact argv, because "no error" is what the old behaviour looked like
// from the outside too — it just dropped the flag.
func TestDeleteForwardsKubectlFlags(t *testing.T) {
	kube := &recordingKubectl{output: ""}
	services := switchServices(t, kube)
	services.Confirm = func(string) error { return nil }
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"nginx"}, kinds.Pod), Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cmd := newDeleteCommand(services)
	cmd.SetArgs([]string{"1", "-y", "--force", "--grace-period=0"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx delete 1 --force --grace-period=0: %v", err)
	}
	if len(kube.runs) != 1 {
		t.Fatalf("kubectl calls = %d, want 1", len(kube.runs))
	}
	if got := joined(kube.runs[0]); got != "delete Pod nginx -n prod --force --grace-period=0" {
		t.Errorf("argv = %q, want the flags forwarded after kx's own", got)
	}
}

func TestScaleForwardsKubectlFlags(t *testing.T) {
	kube := &recordingKubectl{output: ""}
	services := switchServices(t, kube)
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"web"}, kinds.Deployment), Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cmd := newScaleCommand(services)
	cmd.SetArgs([]string{"1", "3", "--timeout=30s"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx scale 1 3 --timeout=30s: %v", err)
	}
	if got := joined(kube.runs[0]); got != "scale Deployment/web --replicas=3 -n prod --timeout=30s" {
		t.Errorf("argv = %q, want --timeout forwarded", got)
	}
}

// --to-revision is the flag that made rollout's missing passthrough hurt:
// `kx rollout undo 1 --to-revision=2` was impossible.
func TestRolloutForwardsKubectlFlags(t *testing.T) {
	kube := &recordingKubectl{output: "deployment.apps/web rolled back"}
	services := switchServices(t, kube)
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"web"}, kinds.Deployment), Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cmd := newRolloutCommand(services)
	cmd.SetArgs([]string{"undo", "1", "--to-revision=2"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx rollout undo 1 --to-revision=2: %v", err)
	}
	if got := joined(kube.runs[0]); got != "rollout undo Deployment/web -n prod --to-revision=2" {
		t.Errorf("argv = %q, want --to-revision forwarded", got)
	}
}

// kx yaml hardcoded -o yaml, so a user's -o arrived alongside it and won only
// by being last. kx now leaves the output format alone once the user names one.
func TestYamlUsesTheCallersOutputFormat(t *testing.T) {
	kube := &recordingKubectl{output: "{}"}
	services := switchServices(t, kube)
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"nginx"}, kinds.Pod), Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cmd := newYamlCommand(services)
	cmd.SetArgs([]string{"1", "-o", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx yaml 1 -o json: %v", err)
	}
	got := joined(kube.runs[0])
	if got != "get Pod nginx -n prod -o json" {
		t.Errorf("argv = %q, want one -o, the caller's", got)
	}
	if strings.Count(got, "-o ") != 1 {
		t.Errorf("argv = %q, want kx's own -o yaml dropped", got)
	}
}

// --show parses the YAML it narrows, so naming another output format is a
// contradiction rather than a refinement.
func TestYamlRefusesShowWithAnExplicitOutputFormat(t *testing.T) {
	kube := &recordingKubectl{output: "{}"}
	services := switchServices(t, kube)
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"nginx"}, kinds.Pod), Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cmd := newYamlCommand(services)
	cmd.SetArgs([]string{"1", "--show", "metadata", "-o", "json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("kx yaml --show -o json succeeded, want a refusal")
	}
	// Asserted on the message, not just on failure: before the flags passed
	// through at all, cobra refused this with "unknown flag: -o" — the right
	// outcome for the wrong reason, which a bare err != nil could not tell
	// apart from the refusal this is testing for.
	if !strings.Contains(err.Error(), "--show") {
		t.Errorf("err = %q, want kx's own refusal naming --show", err)
	}
	if len(kube.runs) != 0 {
		t.Errorf("made %d kubectl calls for a refused command, want 0", len(kube.runs))
	}
}

// kx builds --replicas from the positional, so a second one contradicts it.
func TestScaleRefusesAnExplicitReplicasFlag(t *testing.T) {
	kube := &recordingKubectl{output: ""}
	services := switchServices(t, kube)
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"web"}, kinds.Deployment), Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cmd := newScaleCommand(services)
	cmd.SetArgs([]string{"1", "3", "--replicas=5"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("kx scale 1 3 --replicas=5 succeeded, want a refusal")
	}
	// The message matters for the same reason it does in the --show case:
	// cobra's "unknown flag" would have passed a bare err != nil check.
	if !strings.Contains(err.Error(), "--replicas") || strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("err = %q, want kx's own refusal naming --replicas", err)
	}
	if len(kube.runs) != 0 {
		t.Errorf("made %d kubectl calls for a refused command, want 0", len(kube.runs))
	}
}

// Cobra's arity check runs against the unstripped argv, so a command whose
// arguments are all kx's own flags reaches its index lookup with nothing —
// and --help is a single argument that an Args gate would reject before
// passthrough could resolve it.
func TestNewlyForwardingCommandsHaveNoArgsValidator(t *testing.T) {
	for name, cmd := range map[string]*cobra.Command{
		"delete":  newDeleteCommand(Services{}),
		"scale":   newScaleCommand(Services{}),
		"rollout": newRolloutCommand(Services{}),
		"yaml":    newYamlCommand(Services{}),
	} {
		if cmd.Args != nil {
			t.Errorf("%s has an Args validator; cobra runs it against the raw argv, "+
				"which counts forwarded kubectl flags as positional arguments", name)
		}
		if !cmd.DisableFlagParsing {
			t.Errorf("%s does not disable flag parsing, so kubectl's flags cannot pass through", name)
		}
	}
}

// A flags-only argv is the shape the missing Args gate lets through, so the
// real "needs an index" check has to live in RunE.
func TestNewlyForwardingCommandsRejectAFlagsOnlyArgv(t *testing.T) {
	for name, spec := range map[string]struct {
		build func(Services) *cobra.Command
		args  []string
	}{
		"delete":  {newDeleteCommand, []string{"-y"}},
		"scale":   {newScaleCommand, []string{"--timeout=30s"}},
		"rollout": {newRolloutCommand, []string{"--timeout=30s"}},
		"yaml":    {newYamlCommand, []string{"--show", "metadata"}},
	} {
		kube := &recordingKubectl{}
		services := switchServices(t, kube)
		cmd := spec.build(services)
		cmd.SetArgs(spec.args)
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		if err := cmd.Execute(); err == nil {
			t.Errorf("kx %s %v succeeded with no index", name, spec.args)
		}
		if calls := len(kube.runs) + len(kube.interactive); calls != 0 {
			t.Errorf("%s made %d kubectl calls with no index", name, calls)
		}
	}
}

// Every flag these commands parse by hand must stay registered, or it works
// and vanishes from --help.
func TestNewlyForwardingCommandsRegisterTheirOwnFlags(t *testing.T) {
	if newDeleteCommand(Services{}).Flags().Lookup("yes") == nil {
		t.Error("delete --yes is not registered, so it is absent from --help")
	}
	if newYamlCommand(Services{}).Flags().Lookup("show") == nil {
		t.Error("yaml --show is not registered, so it is absent from --help")
	}
}

// Forwarding --dry-run made kx's own success message reachable for a delete
// that did not happen: "✓ Deleted Pod/web-healthy-…" for a client-side dry
// run. kx replaces kubectl's output with its own line, so the line has to say
// which it was.
func TestDeleteSaysWhenItWasADryRun(t *testing.T) {
	for _, value := range []string{"--dry-run=client", "--dry-run=server"} {
		kube := &recordingKubectl{output: ""}
		message, err := DeleteCommand{
			Kubectl: kube, State: pod("nginx"), Confirm: func(string) error { return nil },
			Status: noStatus,
		}.Execute(1, true, []string{value})
		if err != nil {
			t.Fatalf("Execute(%s): %v", value, err)
		}
		if !strings.Contains(message, "dry run") {
			t.Errorf("message = %q for %s, want it to say nothing was deleted", message, value)
		}
	}
}

// Only the two values that mean a dry run are labelled. Anything else —
// --dry-run=none, or a spelling kubectl may add — is left unlabelled rather
// than guessed at: a wrong "(dry run)" on a real delete is the worse failure,
// and kx does not otherwise read kubectl's flag semantics.
func TestDeleteDoesNotClaimADryRunItCannotConfirm(t *testing.T) {
	for _, args := range [][]string{{"--dry-run=none"}, {"--force"}, nil} {
		message, err := DeleteCommand{
			Kubectl: &recordingKubectl{}, State: pod("nginx"),
			Confirm: func(string) error { return nil }, Status: noStatus,
		}.Execute(1, true, args)
		if err != nil {
			t.Fatalf("Execute(%v): %v", args, err)
		}
		if strings.Contains(message, "dry run") {
			t.Errorf("message = %q for %v, want no dry-run claim", message, args)
		}
	}
}

// An index named twice is one resource. Overlapping ranges are how this
// actually happens — `kx labels 1..3 2..4` printed 2 and 3 twice — and for
// kx delete a repeat meant a second delete of something already gone.
func TestParseIndexesDropsRepeats(t *testing.T) {
	resolver := fakeResolver{name: "web", namespace: "prod", kind: kinds.Pod, count: 10}

	for _, tc := range []struct {
		args []string
		want []int
	}{
		{[]string{"2", "2", "2"}, []int{2}},
		{[]string{"1..3", "2..4"}, []int{1, 2, 3, 4}},
		{[]string{"3", "1", "3"}, []int{3, 1}},
		{[]string{"2..4", "3"}, []int{2, 3, 4}},
	} {
		got, err := parseIndexes(resolver, "indexes", tc.args)
		if err != nil {
			t.Fatalf("parseIndexes(%v): %v", tc.args, err)
		}
		if len(got) != len(tc.want) {
			t.Errorf("parseIndexes(%v) = %v, want %v", tc.args, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("parseIndexes(%v) = %v, want %v — first occurrence wins, in order",
					tc.args, got, tc.want)
				break
			}
		}
	}
}

// A closed range that overshoots the listing is clamped to it, the way the
// open-ended form already is: `13..` ends at the last row, and `13..20`
// failing outright on the same listing was the inconsistency.
func TestExpandRangeClampsAClosedRangeToTheListing(t *testing.T) {
	resolver := fakeResolver{name: "web", namespace: "prod", kind: kinds.Pod, count: 14}

	for _, tc := range []struct {
		arg  string
		want []int
	}{
		{"13..20", []int{13, 14}},
		{"1..100", nil}, // 1..14, checked by length below
		{"20..13", []int{14, 13}},
		{"0..3", []int{1, 2, 3}},
	} {
		got, ok, err := expandRange(resolver, "indexes", tc.arg)
		if !ok || err != nil {
			t.Fatalf("expandRange(%q) ok=%v err=%v", tc.arg, ok, err)
		}
		if tc.want == nil {
			if len(got) != 14 || got[0] != 1 || got[13] != 14 {
				t.Errorf("expandRange(%q) = %v, want the whole 14-row listing", tc.arg, got)
			}
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("expandRange(%q) = %v, want %v", tc.arg, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("expandRange(%q) = %v, want %v", tc.arg, got, tc.want)
				break
			}
		}
	}
}

// Clamping to nothing is not a silent no-op: a range entirely past the end
// gets the sentence the open-ended form already uses, rather than a command
// that appears to succeed having done nothing.
func TestExpandRangeRefusesARangeEntirelyPastTheListing(t *testing.T) {
	resolver := fakeResolver{name: "web", namespace: "prod", kind: kinds.Pod, count: 14}

	for _, arg := range []string{"20..30", "30..20"} {
		_, ok, err := expandRange(resolver, "indexes", arg)
		if !ok {
			t.Fatalf("expandRange(%q) was not recognised as a range", arg)
		}
		if err == nil {
			t.Errorf("expandRange(%q) = nil error, want a refusal", arg)
			continue
		}
		if !strings.Contains(err.Error(), "starts past the current listing") {
			t.Errorf("expandRange(%q) error = %q, want the past-the-listing sentence", arg, err)
		}
	}
}

// The open-ended form must keep behaving exactly as it did — it is the
// behaviour the closed form is being brought into line with.
func TestExpandRangeOpenEndStillEndsAtTheListing(t *testing.T) {
	resolver := fakeResolver{name: "web", namespace: "prod", kind: kinds.Pod, count: 14}

	got, ok, err := expandRange(resolver, "indexes", "13..")
	if !ok || err != nil {
		t.Fatalf("expandRange(13..) ok=%v err=%v", ok, err)
	}
	if len(got) != 2 || got[0] != 13 || got[1] != 14 {
		t.Errorf("expandRange(13..) = %v, want [13 14]", got)
	}
	if _, _, err := expandRange(resolver, "indexes", "20.."); err == nil {
		t.Error("expandRange(20..) = nil error, want the past-the-listing refusal")
	}
}
