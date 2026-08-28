package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/state"
)

// topServices builds a Services that can drive newTopCommand's RunE all the
// way through, with a real (temp-file-backed) state.Service — Services.State
// is a concrete *state.Service, not an interface, so a fakeState substitute
// (used by the TopCommand.Execute-level tests above) does not fit here.
func topServices(t *testing.T, kubectl kubectl.Service) Services {
	t.Helper()
	return Services{
		Kubectl: kubectl,
		State:   &state.Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")},
		Index:   indexService(),
		Config:  config.Default(),
	}
}

const topOutput = "NAME             CPU(cores)   MEMORY(bytes)\n" +
	"web-1            5m           64Mi\n" +
	"web-2            250m         200Mi"

const nodesOutput = "NAME       CPU(cores)   CPU(%)   MEMORY(bytes)   MEMORY(%)\n" +
	"node-a     196m         1%       1864Mi          24%\n" +
	"node-b     500m         5%       3200Mi          40%"

// Two different namespaces, same pod name — the exact collision bare-name
// keying can't handle, and the composite-key fix exists to cover.
const topAllNamespacesOutput = "NAMESPACE     NAME     CPU(cores)   MEMORY(bytes)\n" +
	"prod          web-1    5m           64Mi\n" +
	"staging       web-1    3m           32Mi"

const allNamespacesPodsJSON = `{"items":[
  {"metadata":{"name":"web-1","namespace":"prod"},"spec":{"containers":[
    {"resources":{"limits":{"cpu":"500m","memory":"128Mi"}}}]}},
  {"metadata":{"name":"web-1","namespace":"staging"},"spec":{"containers":[
    {"resources":{"limits":{"cpu":"500m","memory":"256Mi"}}}]}}
]}`

// Two containers, one with limits on both resources and one missing a memory
// limit, so the summing and the "undefined" rule are both exercised.
const podsJSON = `{"items":[
  {"metadata":{"name":"web-1","namespace":"prod"},"spec":{"containers":[
    {"resources":{"limits":{"cpu":"500m","memory":"128Mi"}}}]}},
  {"metadata":{"name":"web-2","namespace":"prod"},"spec":{"containers":[
    {"resources":{"limits":{"cpu":"500m","memory":"256Mi"}}},
    {"resources":{"limits":{"cpu":"500m"}}}]}}
]}`

// scriptedKubectl answers each Run in sequence, so a command making several
// calls can be driven without matching on arguments.
type scriptedKubectl struct {
	outputs []string
	calls   [][]string
	probes  [][]string
	// probeCode is returned by every Probe call; zero value (0) means
	// "available", so every existing test using scriptedKubectl without
	// setting this keeps behaving exactly as it does today.
	probeCode int
	// namespaceCalls counts CurrentNamespace, which shells out to
	// `kubectl config view` and so is worth not doing twice per command.
	namespaceCalls int
}

func (k *scriptedKubectl) Run(args []string) (string, error) {
	k.calls = append(k.calls, args)
	if len(k.calls)-1 < len(k.outputs) {
		return k.outputs[len(k.calls)-1], nil
	}
	return "", nil
}
func (k *scriptedKubectl) RunInteractive([]string, bool) (int, error) { return 0, nil }
func (k *scriptedKubectl) Probe(args []string) int {
	k.probes = append(k.probes, args)
	return k.probeCode
}
func (k *scriptedKubectl) Watch([]string, func(string) error) error { return nil }
func (k *scriptedKubectl) CurrentNamespace() string {
	k.namespaceCalls++
	return "prod"
}
func (k *scriptedKubectl) CurrentContext() string { return "test" }

func TestTopEnsureAvailableProbesTheMetricsAPI(t *testing.T) {
	kubectl := &scriptedKubectl{}
	if err := (TopCommand{Kubectl: kubectl}).EnsureAvailable(); err != nil {
		t.Fatalf("EnsureAvailable: %v", err)
	}
	want := "get --raw /apis/metrics.k8s.io/v1beta1"
	if len(kubectl.probes) != 1 || joinArgs(kubectl.probes[0]) != want {
		t.Errorf("probes = %v, want one call to %q", kubectl.probes, want)
	}
}

func TestTopEnsureAvailableReturnsAFriendlyErrorWhenMissing(t *testing.T) {
	kubectl := &scriptedKubectl{probeCode: 1}
	err := (TopCommand{Kubectl: kubectl}).EnsureAvailable()
	if err == nil {
		t.Fatal("EnsureAvailable returned nil with the metrics API unavailable")
	}
	if !strings.Contains(err.Error(), "metrics-server is not available") {
		t.Errorf("err = %q, want it to name metrics-server", err.Error())
	}
	if !strings.Contains(err.Error(), "https://github.com/kubernetes-sigs/metrics-server") {
		t.Errorf("err = %q, want an install link", err.Error())
	}
}

// The pods path already existed before this preflight; it must gain the
// check without changing its Run-call sequence for the success path.
func TestTopExecuteFailsFastWhenMetricsAPIIsUnavailable(t *testing.T) {
	kubectl := &scriptedKubectl{probeCode: 1, outputs: []string{topOutput, podsJSON}}
	_, _, err := TopCommand{Kubectl: kubectl, State: &fakeState{}, Index: indexService()}.
		Execute("", nil, false)
	if err == nil {
		t.Fatal("Execute succeeded with the metrics API unavailable")
	}
	if !strings.Contains(err.Error(), "metrics-server is not available") {
		t.Errorf("err = %q, want the friendly message", err.Error())
	}
	if len(kubectl.calls) != 0 {
		t.Errorf("Run was called %d times, want 0 — the preflight should fail before any kubectl top/get call", len(kubectl.calls))
	}
}

func TestExecuteNodesRelabelsPercentColumnsAndIndexes(t *testing.T) {
	kubectl := &scriptedKubectl{outputs: []string{nodesOutput}}
	states := &fakeState{}
	output, namespace, err := TopCommand{
		Kubectl: kubectl, State: states, Index: indexService(),
	}.ExecuteNodes("", nil)
	if err != nil {
		t.Fatalf("ExecuteNodes: %v", err)
	}
	// A Node is cluster-scoped: kx get nodes records no namespace for one, and
	// kx top nodes must record the same, or the same index diagnoses differently
	// depending on which command handed it out.
	if namespace != "" {
		t.Errorf("namespace = %q, want empty — a Node is not in a namespace", namespace)
	}
	if !strings.Contains(output.Text(), "CPU%") || !strings.Contains(output.Text(), "MEM%") {
		t.Errorf("output = %q, want relabeled CPU%%/MEM%% headers", output)
	}
	if strings.Contains(output.Text(), "CPU(%)") || strings.Contains(output.Text(), "MEMORY(%)") {
		t.Errorf("output = %q, want kubectl's native (%%) headers gone", output)
	}
	if len(states.saved) != 1 {
		t.Fatalf("saved %d state entries, want 1", len(states.saved))
	}
	if kind, _ := states.saved[0].Resources.Kind("node-a"); kind != kinds.Node {
		t.Errorf("kind = %q, want Node", kind)
	}
}

// -m/--match is registered as a general `kx top` flag with no note that it's
// pods-only, so it must actually filter the nodes path too — not silently
// list everything while the user believes they narrowed it down.
func TestExecuteNodesFiltersByMatchTerm(t *testing.T) {
	kubectl := &scriptedKubectl{outputs: []string{nodesOutput}}
	states := &fakeState{}
	output, _, err := TopCommand{
		Kubectl: kubectl, State: states, Index: indexService(),
	}.ExecuteNodes("node-a", nil)
	if err != nil {
		t.Fatalf("ExecuteNodes: %v", err)
	}
	if !strings.Contains(output.Text(), "node-a") {
		t.Errorf("output = %q, want node-a", output)
	}
	if strings.Contains(output.Text(), "node-b") {
		t.Errorf("output = %q, want node-b filtered out", output)
	}
	if len(states.saved) != 1 {
		t.Fatalf("saved %d state entries, want 1", len(states.saved))
	}
	if match := states.saved[0].Query.Match; match == nil || *match != "node-a" {
		t.Errorf("saved match = %v, want \"node-a\" — a stale entry must refresh with the same filter", match)
	}
}

// kubectl top nodes already reports percentages against node capacity
// natively — unlike pods, ExecuteNodes must never fetch or compute limits.
func TestExecuteNodesNeverFetchesLimits(t *testing.T) {
	kubectl := &scriptedKubectl{outputs: []string{nodesOutput}}
	if _, _, err := (TopCommand{Kubectl: kubectl, State: &fakeState{}, Index: indexService()}).
		ExecuteNodes("", nil); err != nil {
		t.Fatalf("ExecuteNodes: %v", err)
	}
	if len(kubectl.calls) != 1 {
		t.Errorf("kubectl.Run called %d times, want 1 (top nodes only, no get pods -o json)", len(kubectl.calls))
	}
	if joinArgs(kubectl.calls[0]) != "top nodes" {
		t.Errorf("args = %q, want \"top nodes\"", joinArgs(kubectl.calls[0]))
	}
}

func TestExecuteNodesFailsFastWhenMetricsAPIIsUnavailable(t *testing.T) {
	kubectl := &scriptedKubectl{probeCode: 1}
	_, _, err := (TopCommand{Kubectl: kubectl, State: &fakeState{}, Index: indexService()}).
		ExecuteNodes("", nil)
	if err == nil || !strings.Contains(err.Error(), "metrics-server is not available") {
		t.Errorf("err = %v, want the friendly metrics-server message", err)
	}
	if len(kubectl.calls) != 0 {
		t.Errorf("Run was called %d times, want 0", len(kubectl.calls))
	}
}

// A bare `kx top` (or any non-"nodes" leading token, e.g. --match's value)
// must be provably unchanged: it stays the pods path with no token
// stripped from what reaches TopCommand.Execute.
func TestTopCommandDefaultsToPods(t *testing.T) {
	kube := &scriptedKubectl{outputs: []string{topOutput, podsJSON}}
	cmd := newTopCommand(topServices(t, kube))
	sink := captureRender(t)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(sink.String(), "Pods") {
		t.Errorf("output = %q, want the Pods caption", sink.String())
	}
}

// Matches kx get -A's own caption override (getbody.go): with many
// namespaces spanned, there is no single one to name in the caption.
func TestTopCommandAllNamespacesCaptionSaysAllNamespaces(t *testing.T) {
	kube := &scriptedKubectl{outputs: []string{topAllNamespacesOutput, allNamespacesPodsJSON}}
	cmd := newTopCommand(topServices(t, kube))
	sink := captureRender(t)
	if err := cmd.RunE(cmd, []string{"-A"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(sink.String(), "all namespaces") {
		t.Errorf("output = %q, want the \"all namespaces\" caption", sink.String())
	}
	if strings.Contains(sink.String(), "· prod ·") {
		t.Errorf("output = %q, want no single namespace named in the caption", sink.String())
	}
	// The note explaining why -A had no indexes is gone, because it does now.
	if !strings.Contains(sink.String(), "X") {
		t.Errorf("output = %q, want an index column", sink.String())
	}
}

func TestTopCommandRoutesNodesToken(t *testing.T) {
	kube := &scriptedKubectl{outputs: []string{nodesOutput}}
	cmd := newTopCommand(topServices(t, kube))
	sink := captureRender(t)
	if err := cmd.RunE(cmd, []string{"nodes"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(sink.String(), "Nodes") {
		t.Errorf("output = %q, want the Nodes caption", sink.String())
	}
	if joinArgs(kube.calls[0]) != "top nodes" {
		t.Errorf("args = %q, want \"top nodes\" (no leftover \"nodes\" positional)", joinArgs(kube.calls[0]))
	}
}

// An explicit "pods" token must be stripped exactly like "nodes" is — not
// left in extraArgs, where it would reach kubectl as a pod-name filter
// (`kubectl top pods pods`, which 404s) instead of a resource type.
func TestTopCommandStripsExplicitPodsToken(t *testing.T) {
	kube := &scriptedKubectl{outputs: []string{topOutput, podsJSON}}
	cmd := newTopCommand(topServices(t, kube))
	sink := captureRender(t)
	if err := cmd.RunE(cmd, []string{"pods"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(sink.String(), "Pods") {
		t.Errorf("output = %q, want the Pods caption", sink.String())
	}
	if joinArgs(kube.calls[0]) != "top pods" {
		t.Errorf("args = %q, want \"top pods\" (no leftover \"pods\" positional)", joinArgs(kube.calls[0]))
	}
}

// topRow is the text topPageRows is handed in production: exactly what
// index.Add produced, since TopCommand.Execute returns that string and the
// caller passes the same one to both render.IndexedTable and here.
//
// Built through Add rather than written out by hand. The literal it replaced
// was the *rendered* shape — indented two spaces, columns spread four apart —
// which nothing ever feeds back into the parser, and which quietly disagreed
// with Format's real output (no indent, gaps of exactly two).
func topRow(t *testing.T, table string) index.Table {
	t.Helper()
	return index.Service{}.Add(table)
}

func TestTopPageRowsParsesIndexedTable(t *testing.T) {
	indexed := topRow(t, "NAME    CPU(cores)   CPU%   MEMORY(bytes)   MEM%\n"+
		"web-1   5m           12%    64Mi            80%")
	rows := topPageRows(indexed)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	row := rows[0]
	if row.Index != 1 || row.Name != "web-1" || row.CPU != "5m" || row.Memory != "64Mi" {
		t.Errorf("row = %+v, want Index 1, Name web-1, CPU 5m, Memory 64Mi", row)
	}
	if !row.CPUPct.Known || row.CPUPct.Pct != 12 {
		t.Errorf("CPUPct = %+v, want Known with Pct 12", row.CPUPct)
	}
	if !row.MemPct.Known || row.MemPct.Pct != 80 {
		t.Errorf("MemPct = %+v, want Known with Pct 80", row.MemPct)
	}
}

// --no-limits skips the limits lookup, so its table has no CPU%/MEM% columns
// and those fields must stay at their zero value rather than misreading a
// neighbouring column.
//
// This used to also assert Index stayed 0, back when an -A table reached here
// unindexed. Every table topPageRows sees now comes from index.Add, which
// prepends the column, so there is no unindexed shape left to degrade from.
func TestTopPageRowsHandlesTableWithNoPercentColumns(t *testing.T) {
	table := index.Service{}.Add("NAMESPACE     NAME     CPU(cores)   MEMORY(bytes)\n" +
		"prod          web-1    5m           64Mi\n")

	rows := topPageRows(table)

	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].CPUPct.Known || rows[0].MemPct.Known {
		t.Errorf("CPUPct/MemPct = %+v/%+v, want both unknown", rows[0].CPUPct, rows[0].MemPct)
	}
	// The columns that are present still have to be read correctly, or
	// "unknown percentages" would pass against a row read one column over.
	if rows[0].Name != "web-1" || rows[0].CPU != "5m" || rows[0].Memory != "64Mi" {
		t.Errorf("row = %+v, want web-1 / 5m / 64Mi", rows[0])
	}
}

// The -A grid needs its own Namespace per row to disambiguate same-named
// pods across namespaces — the same reason podLimits/withUsagePercentages
// key by namespace/name instead of bare name.
func TestTopPageRowsPopulatesNamespaceFromTheNamespaceColumn(t *testing.T) {
	indexed := index.Service{}.Add("NAMESPACE     NAME     CPU(cores)   CPU%    MEMORY(bytes)   MEM%  \n" +
		"prod          web-1    5m           1%      64Mi             50%   \n" +
		"staging       web-1    3m           0%      32Mi             12%   \n")
	rows := topPageRows(indexed)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].Namespace != "prod" || rows[1].Namespace != "staging" {
		t.Errorf("namespaces = %q, %q, want prod, staging", rows[0].Namespace, rows[1].Namespace)
	}
}

// The single-namespace case has no NAMESPACE column at all — Namespace
// stays empty rather than defaulting to something misleading, and the grid
// only shows the column when at least one row actually has one.
func TestTopPageRowsLeavesNamespaceEmptyWithoutANamespaceColumn(t *testing.T) {
	indexed := topRow(t, "NAME    CPU(cores)   CPU%   MEMORY(bytes)   MEM%\n"+
		"web-1   5m           12%    64Mi            80%")
	rows := topPageRows(indexed)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Namespace != "" {
		t.Errorf("Namespace = %q, want empty (no NAMESPACE column in single-namespace mode)", rows[0].Namespace)
	}
	// Asserting only on Namespace let this pass against a fixture whose columns
	// were shifted by one, since the shifted row had no NAMESPACE column either.
	// Pin a value that moves when the columns move.
	if rows[0].Name != "web-1" {
		t.Errorf("Name = %q, want web-1 — columns are misaligned", rows[0].Name)
	}
}

func TestTopRegistersHTMLFlags(t *testing.T) {
	cmd := newTopCommand(Services{})
	for _, name := range []string{"html", "port", "no-open"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("--%s is not registered, so it will not appear in --help", name)
		}
	}
}

// Regression shape already pinned for diag/scan/tree: --html must add to
// the terminal output, never replace it.
func TestTopWithHTMLStillPrintsTheTerminalTable(t *testing.T) {
	kube := &scriptedKubectl{outputs: []string{topOutput, podsJSON}}
	cmd := newTopCommand(topServices(t, kube))
	cmd.SetContext(stoppedContext())
	sink := captureRender(t)
	// --html/--no-open must go through args, not cmd.Flags().Set: top uses
	// DisableFlagParsing (like scan), so these are extracted by hand from
	// the raw argv, not populated by cobra's own flag parsing. Setting the
	// registered flag directly would pass even if the hand-extraction were
	// broken (as it briefly was) since real invocations never populate the
	// flag that way.
	if err := cmd.RunE(cmd, []string{"--html", "--no-open"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(sink.String(), "Pods") {
		t.Errorf("terminal output = %q, want the table to still print with --html set", sink.String())
	}
}

// The real bug this guards: --html/--port/--no-open must be stripped from
// the args before they reach kubectl, the same way --match/--no-limits
// already are. If extraction is skipped, kubectl sees "--html" itself.
func TestTopHTMLFlagsAreStrippedBeforeReachingKubectl(t *testing.T) {
	kube := &scriptedKubectl{outputs: []string{topOutput, podsJSON}}
	cmd := newTopCommand(topServices(t, kube))
	cmd.SetContext(stoppedContext())
	captureRender(t)
	if err := cmd.RunE(cmd, []string{"--html", "--no-open", "--port", "0"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	for _, call := range kube.calls {
		args := joinArgs(call)
		if strings.Contains(args, "--html") || strings.Contains(args, "--port") || strings.Contains(args, "--no-open") {
			t.Errorf("kubectl called with %q, want no leftover --html/--port/--no-open", args)
		}
	}
}

func TestTopAppendsUsagePercentages(t *testing.T) {
	kubectl := &scriptedKubectl{outputs: []string{topOutput, podsJSON}}
	states := &fakeState{}
	output, _, err := TopCommand{Kubectl: kubectl, State: states, Index: indexService()}.
		Execute("", nil, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(output.Text(), "CPU%") || !strings.Contains(output.Text(), "MEM%") {
		t.Fatalf("output has no percentage columns:\n%s", output)
	}
	// web-1: 5m of 500m = 1%, 64Mi of 128Mi = 50%.
	if !strings.Contains(output.Text(), "1%") || !strings.Contains(output.Text(), "50%") {
		t.Errorf("web-1 percentages are wrong:\n%s", output)
	}
}

// A container missing a limit makes that resource undefined for the whole pod:
// a percentage against a partial denominator reads as headroom that isn't there.
func TestTopMarksPartialLimitsUndefined(t *testing.T) {
	kubectl := &scriptedKubectl{outputs: []string{topOutput, podsJSON}}
	output, _, err := TopCommand{Kubectl: kubectl, State: &fakeState{}, Index: indexService()}.
		Execute("", nil, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// web-2's CPU limit sums to 1000m across both containers (250m = 25%), but
	// its memory limit is undefined because the second container has none.
	var row string
	for _, line := range strings.Split(output.Text(), "\n") {
		if strings.HasPrefix(line, "2 ") || strings.Contains(line, "web-2") {
			row = line
		}
	}
	if !strings.Contains(row, "25%") {
		t.Errorf("web-2 CPU%% is wrong: %q", row)
	}
	if !strings.Contains(row, "—") {
		t.Errorf("web-2 memory should be undefined: %q", row)
	}
}

// --no-limits skips the extra kubectl call entirely.
func TestTopNoLimitsSkipsTheLimitsCall(t *testing.T) {
	kubectl := &scriptedKubectl{outputs: []string{topOutput}}
	output, _, err := TopCommand{Kubectl: kubectl, State: &fakeState{}, Index: indexService()}.
		Execute("", nil, true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(output.Text(), "CPU%") {
		t.Errorf("--no-limits still added percentage columns:\n%s", output)
	}
	if len(kubectl.calls) != 1 {
		t.Errorf("made %d kubectl calls, want 1", len(kubectl.calls))
	}
}

// --containers is a different table shape, so percentages don't apply.
func TestTopContainersFlagSkipsPercentages(t *testing.T) {
	kubectl := &scriptedKubectl{outputs: []string{topOutput}}
	output, _, err := TopCommand{Kubectl: kubectl, State: &fakeState{}, Index: indexService()}.
		Execute("", []string{"--containers"}, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(output.Text(), "CPU%") {
		t.Errorf("--containers still added percentage columns:\n%s", output)
	}
}

// Names aren't unique across namespaces, so -A output is never indexed.
// -A is still never indexed or saved to state — names aren't unique across
// namespaces — even though it now computes percentages same as any other
// top listing.
// `kx top -A` indexes like `kx get -A` now that a resource carries its own
// namespace — the two have always shared this rule, including while it was
// "no indexes for -A". The fixture's two rows share the name web-1, so the
// namespace is the only thing telling them apart.
func TestTopAllNamespacesIsIndexed(t *testing.T) {
	kubectl := &scriptedKubectl{outputs: []string{topAllNamespacesOutput, allNamespacesPodsJSON}}
	states := &fakeState{}
	output, _, err := TopCommand{Kubectl: kubectl, State: states, Index: indexService()}.
		Execute("", []string{"-A"}, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	headers, _, _ := index.ParseTable(output.Text())
	if index.ColumnIndex(headers, "X") < 0 {
		t.Errorf("-A output was not indexed:\n%s", output)
	}
	if len(states.saved) != 1 {
		t.Fatalf("saved %d entries, want 1", len(states.saved))
	}
	entries := states.saved[0].Resources.Entries()
	if len(entries) != 2 {
		t.Fatalf("saved %d resources, want 2 (same-named rows must not collapse)", len(entries))
	}
	if entries[0].Namespace != "prod" || entries[1].Namespace != "staging" {
		t.Errorf("namespaces = %q, %q; want prod, staging",
			entries[0].Namespace, entries[1].Namespace)
	}
}

// The entry-level namespace stays empty for -A, so Fields' fallback never hands
// one listing's namespace to a row that belongs to another.
func TestTopAllNamespacesLeavesTheEntryNamespaceEmpty(t *testing.T) {
	kubectl := &scriptedKubectl{outputs: []string{topAllNamespacesOutput, allNamespacesPodsJSON}}
	states := &fakeState{}
	if _, _, err := (TopCommand{Kubectl: kubectl, State: states, Index: indexService()}).
		Execute("", []string{"-A"}, false); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := states.saved[0].Namespace; got != "" {
		t.Errorf("entry namespace = %q, want empty for an -A listing", got)
	}
}

func TestTopAllNamespacesGetsPercentagesKeyedByNamespace(t *testing.T) {
	kubectl := &scriptedKubectl{outputs: []string{topAllNamespacesOutput, allNamespacesPodsJSON}}
	output, _, err := TopCommand{Kubectl: kubectl, State: &fakeState{}, Index: indexService()}.
		Execute("", []string{"-A"}, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// prod/web-1: 5m of 500m = 1%, 64Mi of 128Mi = 50%.
	if !strings.Contains(output.Text(), "1%") || !strings.Contains(output.Text(), "50%") {
		t.Errorf("output missing prod/web-1's percentages:\n%s", output)
	}
	// staging/web-1: 3m of 500m = 0%, 32Mi of 256Mi = 12%. Same pod name,
	// different namespace, different limits — proves the lookup is keyed
	// by namespace, not just name (bare-name keying would give both rows
	// whichever limit was inserted into the map last).
	if !strings.Contains(output.Text(), "12%") {
		t.Errorf("output missing staging/web-1's own percentage (12%%), got same as prod's — composite key not applied:\n%s", output)
	}
	if joinArgs(kubectl.calls[1]) != "get pods -A -o json" {
		t.Errorf("limits call = %q, want \"get pods -A -o json\"", joinArgs(kubectl.calls[1]))
	}
}

func TestTopAllNamespacesNoLimitsStillSkipsPercentages(t *testing.T) {
	kubectl := &scriptedKubectl{outputs: []string{topAllNamespacesOutput}}
	output, _, err := TopCommand{Kubectl: kubectl, State: &fakeState{}, Index: indexService()}.
		Execute("", []string{"-A"}, true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Asserted on the columns rather than on the whole string: -A output is
	// indexed now, so it is no longer byte-identical to kubectl's, and the
	// subject here is the absence of the percentage columns and of the lookup
	// that fills them.
	headers, _, _ := index.ParseTable(output.Text())
	if index.ColumnIndex(headers, "CPU%") >= 0 || index.ColumnIndex(headers, "MEM%") >= 0 {
		t.Errorf("--no-limits -A gained percentage columns:\n%s", output)
	}
	if len(kubectl.calls) != 1 {
		t.Errorf("kubectl called %d times, want 1 (no limits lookup with --no-limits)", len(kubectl.calls))
	}
}

// The saved query is a `get pods` listing, which is what the indexes were
// assigned against, so a stale entry refreshes into something usable.
func TestTopSavesPodsQuery(t *testing.T) {
	kubectl := &scriptedKubectl{outputs: []string{topOutput, podsJSON}}
	states := &fakeState{}
	if _, _, err := (TopCommand{Kubectl: kubectl, State: states, Index: indexService()}).
		Execute("", nil, false); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(states.saved) != 1 {
		t.Fatalf("saved %d entries, want 1", len(states.saved))
	}
	if query := states.saved[0].Query; query == nil || query.Resource != "pods" {
		t.Errorf("Query = %+v, want a pods query", query)
	}
	if kind, _ := states.saved[0].Resources.Kind("web-1"); kind != "Pod" {
		t.Errorf("kind = %q, want Pod", kind)
	}
}

func TestPercentCell(t *testing.T) {
	quantity := func(s string) *resource.Quantity {
		q := resource.MustParse(s)
		return &q
	}
	cases := []struct {
		usage string
		limit *resource.Quantity
		want  string
	}{
		{"5m", quantity("500m"), "1%"},
		{"250m", quantity("1"), "25%"},
		{"64Mi", quantity("128Mi"), "50%"},
		{"120Mi", quantity("128Mi"), "93%"},
		// No limit to measure against.
		{"5m", nil, "—"},
		{"5m", quantity("0"), "—"},
	}
	for _, tc := range cases {
		if got := percentCell(tc.usage, tc.limit); got != tc.want {
			t.Errorf("percentCell(%q, %v) = %q, want %q", tc.usage, tc.limit, got, tc.want)
		}
	}
}

// Sub-core usage must not truncate to zero, which is what comparing whole cores
// would do.
func TestPercentCellKeepsSubCorePrecision(t *testing.T) {
	limit := resource.MustParse("2")
	if got := percentCell("40m", &limit); got != "2%" {
		t.Errorf("percentCell(40m, 2) = %q, want 2%%", got)
	}
}

// Execute returns the namespace it listed from so the caller does not resolve
// it a second time; each resolution is a `kubectl config view` subprocess.
func TestTopReturnsTheNamespaceItUsed(t *testing.T) {
	kubectl := &scriptedKubectl{outputs: []string{topOutput, podsJSON}}
	_, namespace, err := TopCommand{
		Kubectl: kubectl, State: &fakeState{}, Index: indexService(),
	}.Execute("", nil, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if namespace != "prod" {
		t.Errorf("namespace = %q, want prod", namespace)
	}
	if kubectl.namespaceCalls != 1 {
		t.Errorf("CurrentNamespace called %d times, want 1", kubectl.namespaceCalls)
	}
}

// An explicit -n needs no lookup at all, and is what the caption must show.
func TestTopPrefersAnExplicitNamespace(t *testing.T) {
	kubectl := &scriptedKubectl{outputs: []string{topOutput, podsJSON}}
	_, namespace, err := TopCommand{
		Kubectl: kubectl, State: &fakeState{}, Index: indexService(),
	}.Execute("", []string{"-n", "staging"}, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if namespace != "staging" {
		t.Errorf("namespace = %q, want staging", namespace)
	}
	if kubectl.namespaceCalls != 0 {
		t.Errorf("CurrentNamespace called %d times with an explicit -n, want 0",
			kubectl.namespaceCalls)
	}
}

// -n/-A are pure kubectl passthrough, parsed by hand, but were never
// registered so they vanished from --help despite being the most-used flags
// on this command.
func TestTopRegistersNamespaceFlags(t *testing.T) {
	cmd := newTopCommand(Services{})
	for _, name := range []string{"namespace", "all-namespaces"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("--%s is not registered, so it will not appear in --help", name)
		}
	}
}

// kx diag's help offers `kx get nodes` and `kx top nodes` as two ways to reach
// the same index, so the entry they save has to be the same entry. It was not:
// #271 blanked the namespace on the get path only, and a Node indexed by
// top carried whichever namespace the caller happened to be standing in.
//
// That is not just a caption. Gather is handed the saved namespace and filters
// the node's warning events by it (diagnostics/service.go), and node events
// live in `default` — so a node indexed by top diagnosed with every warning
// event missing, while the same node indexed by get showed them.
func TestTopNodesAndGetNodesSaveTheSameNamespace(t *testing.T) {
	tops := &fakeState{}
	if _, _, err := (TopCommand{
		Kubectl: &scriptedKubectl{outputs: []string{nodesOutput}},
		State:   tops, Index: indexService(),
	}).ExecuteNodes("", nil); err != nil {
		t.Fatalf("ExecuteNodes: %v", err)
	}

	gets := &fakeState{}
	if _, _, err := (GetCommand{
		Kubectl: &fakeKubectl{namespace: "prod", output: nodesOutput},
		State:   gets, Index: indexService(),
	}).Execute("nodes", "", nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(tops.saved) != 1 || len(gets.saved) != 1 {
		t.Fatalf("saved %d/%d entries, want 1 each", len(tops.saved), len(gets.saved))
	}
	if tops.saved[0].Namespace != gets.saved[0].Namespace {
		t.Errorf("kx top nodes saved namespace %q, kx get nodes saved %q — the same index must resolve the same way",
			tops.saved[0].Namespace, gets.saved[0].Namespace)
	}
	if tops.saved[0].Namespace != "" {
		t.Errorf("saved namespace = %q, want empty", tops.saved[0].Namespace)
	}
}
