package cli

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
)

const topOutput = "NAME             CPU(cores)   MEMORY(bytes)\n" +
	"web-1            5m           64Mi\n" +
	"web-2            250m         200Mi"

// Two containers, one with limits on both resources and one missing a memory
// limit, so the summing and the "undefined" rule are both exercised.
const podsJSON = `{"items":[
  {"metadata":{"name":"web-1"},"spec":{"containers":[
    {"resources":{"limits":{"cpu":"500m","memory":"128Mi"}}}]}},
  {"metadata":{"name":"web-2"},"spec":{"containers":[
    {"resources":{"limits":{"cpu":"500m","memory":"256Mi"}}},
    {"resources":{"limits":{"cpu":"500m"}}}]}}
]}`

// scriptedKubectl answers each Run in sequence, so a command making several
// calls can be driven without matching on arguments.
type scriptedKubectl struct {
	outputs []string
	calls   [][]string
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
func (k *scriptedKubectl) Probe([]string) int                         { return 0 }
func (k *scriptedKubectl) Watch([]string, func(string) error) error   { return nil }
func (k *scriptedKubectl) CurrentNamespace() string {
	k.namespaceCalls++
	return "prod"
}
func (k *scriptedKubectl) CurrentContext() string { return "test" }

func TestTopAppendsUsagePercentages(t *testing.T) {
	kubectl := &scriptedKubectl{outputs: []string{topOutput, podsJSON}}
	states := &fakeState{}
	output, _, err := TopCommand{Kubectl: kubectl, State: states, Index: indexService()}.
		Execute("", nil, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(output, "CPU%") || !strings.Contains(output, "MEM%") {
		t.Fatalf("output has no percentage columns:\n%s", output)
	}
	// web-1: 5m of 500m = 1%, 64Mi of 128Mi = 50%.
	if !strings.Contains(output, "1%") || !strings.Contains(output, "50%") {
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
	for _, line := range strings.Split(output, "\n") {
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
	if strings.Contains(output, "CPU%") {
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
	if strings.Contains(output, "CPU%") {
		t.Errorf("--containers still added percentage columns:\n%s", output)
	}
}

// Names aren't unique across namespaces, so -A output is never indexed.
func TestTopAllNamespacesIsNotIndexed(t *testing.T) {
	kubectl := &scriptedKubectl{outputs: []string{topOutput}}
	states := &fakeState{}
	output, _, err := TopCommand{Kubectl: kubectl, State: states, Index: indexService()}.
		Execute("", []string{"-A"}, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if output != topOutput {
		t.Errorf("-A output was indexed:\n%s", output)
	}
	if len(states.saved) != 0 {
		t.Errorf("-A saved state, want none")
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
