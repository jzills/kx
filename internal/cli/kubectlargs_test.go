package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/jzills/kx/internal/kubectlflags"
	"github.com/jzills/kx/internal/state"
)

// runGetArgv runs kx get on argv, as a user would type it, against a fake
// kubectl, and returns what kubectl was asked to do.
func runGetArgv(t *testing.T, args ...string) (*recordingKubectl, Services, error) {
	t.Helper()
	kube := &recordingKubectl{output: podsOutput}
	services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})
	cmd := newGetCommand(services)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	_, _, err := runCaptured(t, cmd, args)
	return kube, services, err
}

// A flag before the resource was saved as the resource: every row of
// `kx get -n prod pods` was stored with kind "-n", and kx ref printed
// "-n/<name>". pflag finds the resource wherever it sits.
func TestGetFindsTheResourceAfterAFlag(t *testing.T) {
	kube, services, err := runGetArgv(t, "-n", "prod", "pods")
	if err != nil {
		t.Fatalf("kx get -n prod pods: %v", err)
	}
	if len(kube.runs) != 1 || joinArgs(kube.runs[0]) != "get pods --namespace=prod" {
		t.Errorf("kubectl = %q, want get pods --namespace=prod", kube.runs)
	}
	entry, err := services.State.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if entry.Query == nil || entry.Query.Resource != "pods" {
		t.Errorf("saved query = %+v, want resource pods", entry.Query)
	}
	if _, _, kind, err := services.State.Fields(1); err != nil || kind != "Pod" {
		t.Errorf("row 1 kind = %q (err %v), want Pod", kind, err)
	}
}

// What kubectl receives is what the user gave it: every occurrence, in order,
// with values untouched. A repeated selector stays repeated, and a JSONPath
// value holding ".." is a value, not an index range.
func TestGetForwardsKubectlFlagsInOrder(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"pods", "-l", "a=b", "-l", "c=d"}, "get pods --selector=a=b --selector=c=d"},
		{[]string{"pods", "-Ao", "wide"}, "get pods --all-namespaces --output=wide"},
		{[]string{"pods", "-o", "jsonpath={..metadata.name}"}, "get pods --output=jsonpath={..metadata.name}"},
		{[]string{"pods", "--chunk-size", "500"}, "get pods --chunk-size=500"},
		// kubectl reads an underscore as a dash, and so does kx.
		{[]string{"pods", "--all_namespaces"}, "get pods --all-namespaces"},
		// kubectl's logging flags, which its main adds outside the command
		// tree, are kubectl's here too.
		{[]string{"pods", "-v", "6"}, "get pods --v=6"},
	} {
		kube, _, err := runGetArgv(t, tc.args...)
		if err != nil {
			t.Errorf("kx get %s: %v", joinArgs(tc.args), err)
			continue
		}
		if len(kube.runs) != 1 || joinArgs(kube.runs[0]) != tc.want {
			t.Errorf("kx get %s: kubectl = %q, want %q", joinArgs(tc.args), kube.runs, tc.want)
		}
	}
}

// A flag newer than kx's table is refused, naming the table's kubectl, and
// nothing runs. Anything after -- is kubectl's, unparsed, which is the way to
// pass it anyway.
func TestGetRefusesAnUnknownFlagAndForwardsAfterDoubleDash(t *testing.T) {
	kube, _, err := runGetArgv(t, "pods", "--some-new-flag")
	if err == nil {
		t.Fatal("kx get pods --some-new-flag succeeded")
	}
	for _, want := range []string{"'--some-new-flag'", kubectlflags.Version, "'get <resource> -- --some-new-flag'"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %q", err, want)
		}
	}
	if len(kube.runs) > 0 {
		t.Errorf("kubectl was called: %q", kube.runs)
	}

	kube, _, err = runGetArgv(t, "pods", "-n", "prod", "--", "--some-new-flag", "x")
	if err != nil {
		t.Fatalf("kx get pods -- --some-new-flag x: %v", err)
	}
	if len(kube.runs) != 1 || joinArgs(kube.runs[0]) != "get pods --namespace=prod --some-new-flag x" {
		t.Errorf("kubectl = %q, want the arguments after -- forwarded as given", kube.runs)
	}
}

// What follows -- is kubectl's, never an index. kx get pods -- 2 fetched row 2
// by name, while kx get pods -o wide -- 2 forwarded the 2: whether it was an
// index turned on a flag that had nothing to do with it.
func TestGetReadsNoIndexAfterDoubleDash(t *testing.T) {
	for _, tc := range []struct {
		command func(Services) *cobra.Command
		args    []string
		want    string
	}{
		{newGetCommand, []string{"pods", "--", "2"}, "get pods 2"},
		{newGetCommand, []string{"pods", "-o", "wide", "--", "2"}, "get pods --output=wide 2"},
		{func(s Services) *cobra.Command { return newSecretCommand(s, "secret", nil) },
			[]string{"--", "2"}, "get secret 2"},
	} {
		kube := &recordingKubectl{output: podsOutput}
		cmd := tc.command(staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}}))
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		if _, _, err := runCaptured(t, cmd, tc.args); err != nil {
			t.Errorf("kx %s %s: %v", cmd.Name(), joinArgs(tc.args), err)
			continue
		}
		if len(kube.runs) != 1 || joinArgs(kube.runs[0]) != tc.want {
			t.Errorf("kx %s %s: kubectl = %q, want %q", cmd.Name(), joinArgs(tc.args), kube.runs, tc.want)
		}
	}
}

func TestGetUnknownShorthandIsNamedAsTyped(t *testing.T) {
	_, _, err := runGetArgv(t, "pods", "-Z")
	if err == nil || !strings.Contains(err.Error(), "'-Z'") {
		t.Errorf("err = %v, want it to name '-Z'", err)
	}
}

// Only kubectl's flags with nothing to list is still a missing resource, and
// kx says so itself; kubectl used to answer `kx get -A` in its own words.
func TestGetWithOnlyFlagsAsksForTheResource(t *testing.T) {
	for _, args := range [][]string{{"-A"}, {"-n", "prod"}, {"--no-headers"}, {"--", "pods"}} {
		kube, _, err := runGetArgv(t, args...)
		if err == nil || !strings.Contains(err.Error(), "requires <resource>") {
			t.Errorf("kx get %s: err = %v, want kx's missing-resource error", joinArgs(args), err)
		}
		if len(kube.runs) > 0 {
			t.Errorf("kx get %s: kubectl was called: %q", joinArgs(args), kube.runs)
		}
	}
}

// -k is kx's --key on kx get, as it always was; kubectl's -k is reachable by
// its long name.
func TestGetKeepsDashKForKey(t *testing.T) {
	cmd := newGetCommand(Services{})
	if flag := cmd.Flags().ShorthandLookup("k"); flag == nil || flag.Name != "key" {
		t.Errorf("-k = %v, want kx's --key", flag)
	}
	if flag := cmd.Flags().Lookup("kustomize"); flag == nil || flag.Shorthand != "" {
		t.Errorf("--kustomize = %+v, want it registered without a shorthand", flag)
	}
}

// A flag kx reads as well as forwards keeps kx's usage text and takes
// kubectl's shape, so it parses exactly as kubectl would.
func TestSharedFlagsTakeKubectlsShape(t *testing.T) {
	cmd := newGetCommand(Services{})
	table := map[string]kubectlflags.Flag{}
	own, _ := kubectlflags.Command("get")
	for _, flag := range append(own, kubectlflags.Global()...) {
		table[flag.Name] = flag
	}
	for _, name := range []string{"namespace", "all-namespaces", "output", "no-headers", "watch"} {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			t.Errorf("--%s is not registered", name)
			continue
		}
		if isKubectlFlag(flag) {
			t.Errorf("--%s is listed as kubectl's, want it with kx's options", name)
		}
		if flag.NoOptDefVal != table[name].NoOptDefVal {
			t.Errorf("--%s NoOptDefVal = %q, want kubectl's %q", name, flag.NoOptDefVal, table[name].NoOptDefVal)
		}
		if strings.Contains(flag.Usage, table[name].Usage) {
			t.Errorf("--%s has kubectl's usage, want kx's own", name)
		}
	}
}

func TestRegisterKubectlFlagPreservesDeprecatedShorthand(t *testing.T) {
	const warning = "use --filename instead"
	source := kubectlflags.Flag{
		Name:                "filename",
		Shorthand:           "f",
		Type:                "string",
		Usage:               "file to read",
		ShorthandDeprecated: warning,
	}
	for _, tc := range []struct {
		name        string
		preexisting bool
	}{
		{name: "new flag"},
		{name: "shared flag", preexisting: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			if tc.preexisting {
				flags.StringP("filename", "f", "", "kx option")
			}
			args := &kubectlArgs{definedBy: map[string][]string{}}
			registerKubectlFlag(flags, args, source, kubectlFlagCommand)

			var output strings.Builder
			flags.SetOutput(&output)
			if err := flags.Parse([]string{"-f", "input.yaml"}); err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !strings.Contains(output.String(), warning) {
				t.Errorf("shorthand warning = %q, want %q", output.String(), warning)
			}
		})
	}
}

func TestRegisterKubectlFlagDoesNotDeprecateDifferentKxShorthand(t *testing.T) {
	const warning = "use --filename instead"
	source := kubectlflags.Flag{
		Name:                "filename",
		Shorthand:           "f",
		Type:                "string",
		Usage:               "file to read",
		ShorthandDeprecated: warning,
	}
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.StringP("filename", "x", "", "kx option")
	args := &kubectlArgs{definedBy: map[string][]string{}}
	registerKubectlFlag(flags, args, source, kubectlFlagCommand)

	flag := flags.Lookup("filename")
	if flag.Shorthand != "x" {
		t.Fatalf("shorthand = %q, want kx's -x", flag.Shorthand)
	}
	if flag.ShorthandDeprecated != "" {
		t.Fatalf("ShorthandDeprecated = %q, want empty for kx's distinct shorthand", flag.ShorthandDeprecated)
	}

	var output strings.Builder
	flags.SetOutput(&output)
	if err := flags.Parse([]string{"-x", "input.yaml"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if strings.Contains(output.String(), warning) {
		t.Errorf("parsing kx's -x emitted kubectl's deprecation warning: %q", output.String())
	}
}

// kubectl's flags are listed apart from kx's, and its thirty global flags are
// named rather than listed.
func TestGetHelpListsKubectlFlagsApart(t *testing.T) {
	help := commandHelp(newGetCommand(Services{}))
	names := func(items []string) string { return strings.Join(items, " ") }
	var options, kubectl []string
	for _, item := range help.Options {
		options = append(options, item.Name)
	}
	for _, item := range help.Kubectl {
		kubectl = append(kubectl, item.Name)
	}
	if strings.Contains(names(options), "--selector") || strings.Contains(names(options), "--kubeconfig") {
		t.Errorf("Options = %q, want kubectl's flags left out", options)
	}
	if !strings.Contains(names(options), "--match") {
		t.Errorf("Options = %q, want kx's own --match", options)
	}
	if !strings.Contains(names(kubectl), "--selector") {
		t.Errorf("kubectl options = %q, want --selector", kubectl)
	}
	if strings.Contains(names(kubectl), "--kubeconfig") {
		t.Errorf("kubectl options = %q, want the global flags named, not listed", kubectl)
	}
	if strings.Contains(help.Doc, "passed through") {
		t.Errorf("help says flags pass through, but kx get refuses one it doesn't know:\n%s", help.Doc)
	}
}

// requiredUse builds the refusal's example from the Use string, so it must
// leave out the optional arguments and the flag placeholder.
func TestRequiredUse(t *testing.T) {
	root := &cobra.Command{Use: "kx"}
	cmd := &cobra.Command{Use: "get <resource> [index]... [kubectl flags]"}
	root.AddCommand(cmd)
	if got := requiredUse(cmd); got != "kx get <resource>" {
		t.Errorf("requiredUse = %q, want %q", got, "kx get <resource>")
	}
}

// Commands share annotation maps — every document command has
// documentAnnotations — so registering kubectl's flags on one must not mark
// the others as converted. Converting kx logs once told kx yaml's help that
// unknown flags are refused.
func TestRegisteringKubectlFlagsLeavesSharedAnnotationsAlone(t *testing.T) {
	shared := map[string]string{"kx/shared": "true"}
	cmd := &cobra.Command{Use: "logs <index>", Annotations: shared}
	registerKubectlFlags(cmd, "logs")
	if _, marked := shared[kubectlCommandAnnotation]; marked {
		t.Error("the shared annotation map was marked as converted")
	}
	if cmd.Annotations[kubectlCommandAnnotation] != "logs" || cmd.Annotations["kx/shared"] != "true" {
		t.Errorf("cmd.Annotations = %v, want its own copy, marked and keeping the shared keys", cmd.Annotations)
	}
}

// A shared flag is documented in kx's terms, its type included: kx logs'
// --since takes 7d, and help calling it a duration would send a reader to
// Go's duration syntax.
func TestSharedFlagsKeepKxsType(t *testing.T) {
	since := newLogsCommand(Services{}).Flags().Lookup("since")
	if since == nil || since.Value.Type() != "string" {
		t.Errorf("--since type = %v, want kx's string", since)
	}
}

// A command that registered kubectl's flags must let cobra parse them — with
// DisableFlagParsing still set, the registered flags would never be set and
// every one would reach RunE as a positional.
func TestConvertedCommandsParseTheirFlags(t *testing.T) {
	converted := 0
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		if recordedKubectlArgs(cmd) != nil {
			converted++
			if cmd.DisableFlagParsing {
				t.Errorf("%s registers kubectl's flags but disables flag parsing", cmd.CommandPath())
			}
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(NewRoot(argvServices(t), "test"))
	if converted == 0 {
		t.Fatal("found no converted command; the walk checked nothing")
	}
}
