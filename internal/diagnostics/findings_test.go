package diagnostics

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/jzills/kx/internal/kinds"
)

// unbounded is the zero window: every finding a report can produce, which is
// what every test that is not about the window itself wants.
var unbounded time.Time

func quantity(value string) *resource.Quantity {
	parsed := resource.MustParse(value)
	return &parsed
}

func summaries(findings []Finding) []string {
	out := make([]string, 0, len(findings))
	for _, finding := range findings {
		out = append(out, finding.Summary)
	}
	return out
}

func hasSummaryContaining(findings []Finding, substring string) bool {
	for _, finding := range findings {
		if strings.Contains(finding.Summary, substring) {
			return true
		}
	}
	return false
}

func severityOf(t *testing.T, findings []Finding, substring string) Severity {
	t.Helper()
	for _, finding := range findings {
		if strings.Contains(finding.Summary, substring) {
			return finding.Severity
		}
	}
	t.Fatalf("no finding contains %q; got %v", substring, summaries(findings))
	return OK
}

// The verdict is the highest finding severity, and findings sort most severe
// first so the triage table's top finding is the worst one.
func TestReportVerdictAndOrdering(t *testing.T) {
	report := BuildReport(Data{
		Replicas: &ReplicaHealth{Desired: 2, Ready: 0, Available: 0, Updated: 2},
	})
	if report.Verdict != Critical {
		t.Errorf("verdict = %v, want Critical", report.Verdict)
	}
	if report.Findings[0].Severity != Critical {
		t.Errorf("findings are not sorted most-severe-first: %v", summaries(report.Findings))
	}
}

func TestHealthyReportHasNoFindings(t *testing.T) {
	report := BuildReport(Data{
		Replicas: &ReplicaHealth{Desired: 2, Ready: 2, Available: 2, Updated: 2},
	})
	if report.Verdict != OK {
		t.Errorf("verdict = %v, want OK", report.Verdict)
	}
	if len(report.Findings) != 0 {
		t.Errorf("findings = %v, want none", summaries(report.Findings))
	}
}

// Zero ready out of a nonzero desired is critical; a partial shortfall is a
// warning.
func TestReplicaShortfallSeverity(t *testing.T) {
	none := replicaFindings(ReplicaHealth{Desired: 2, Ready: 0, Available: 0, Updated: 2})
	if got := severityOf(t, none, "0/2 replicas ready"); got != Critical {
		t.Errorf("severity = %v, want Critical", got)
	}

	partial := replicaFindings(ReplicaHealth{Desired: 2, Ready: 1, Available: 1, Updated: 2})
	if got := severityOf(t, partial, "1/2 replicas ready"); got != Warning {
		t.Errorf("severity = %v, want Warning", got)
	}
}

// A Deployment scaled to zero is not broken.
func TestScaledToZeroIsHealthy(t *testing.T) {
	if findings := replicaFindings(ReplicaHealth{Desired: 0}); len(findings) != 0 {
		t.Errorf("findings = %v, want none", summaries(findings))
	}
}

func TestStalledRolloutIsCritical(t *testing.T) {
	generation, observed := int64(5), int64(3)
	findings := replicaFindings(ReplicaHealth{
		Desired: 1, Ready: 1, Available: 1, Updated: 1,
		Generation: &generation, ObservedGeneration: &observed,
	})
	if got := severityOf(t, findings, "Rollout stalled"); got != Critical {
		t.Errorf("severity = %v, want Critical", got)
	}
}

// Suspended, active and completed Jobs are all fine; only the terminal failure
// conditions are findings.
func TestJobFindings(t *testing.T) {
	for _, job := range []JobHealth{
		{Suspended: true},
		{Active: 1},
		{Succeeded: 1},
	} {
		if findings := jobFindings(job, unbounded); len(findings) != 0 {
			t.Errorf("job %+v produced %v, want none", job, summaries(findings))
		}
	}

	failed := jobFindings(JobHealth{Failed: 6, BackoffLimit: 5, BackoffLimitExceeded: true}, unbounded)
	if got := severityOf(t, failed, "BackoffLimitExceeded"); got != Critical {
		t.Errorf("severity = %v, want Critical", got)
	}
	if !hasSummaryContaining(failed, "(6/5 failed)") {
		t.Errorf("findings = %v, want the failure counts", summaries(failed))
	}
}

// A missing selector is a legitimate configuration (ExternalName, headless),
// not a defect.
func TestServiceWithoutSelectorHasNoFindings(t *testing.T) {
	if findings := serviceFindings(ServiceHealth{HasSelector: false}); len(findings) != 0 {
		t.Errorf("findings = %v, want none", summaries(findings))
	}
}

func TestServiceEndpointFindings(t *testing.T) {
	none := serviceFindings(ServiceHealth{HasSelector: true})
	if got := severityOf(t, none, "No endpoints"); got != Critical {
		t.Errorf("severity = %v, want Critical", got)
	}

	noneReady := serviceFindings(ServiceHealth{HasSelector: true, NotReadyAddresses: 2})
	if got := severityOf(t, noneReady, "not ready, 0 ready"); got != Critical {
		t.Errorf("severity = %v, want Critical", got)
	}

	partial := serviceFindings(ServiceHealth{
		HasSelector: true, ReadyAddresses: 1, NotReadyAddresses: 1})
	if got := severityOf(t, partial, "1/2 endpoints ready"); got != Warning {
		t.Errorf("severity = %v, want Warning", got)
	}

	healthy := serviceFindings(ServiceHealth{HasSelector: true, ReadyAddresses: 2})
	if len(healthy) != 0 {
		t.Errorf("findings = %v, want none", summaries(healthy))
	}
}

func TestPVCFindings(t *testing.T) {
	if got := severityOf(t, pvcFindings(PVCHealth{Phase: "Pending"}), "pending"); got != Warning {
		t.Errorf("severity = %v, want Warning", got)
	}
	if got := severityOf(t, pvcFindings(PVCHealth{Phase: "Lost"}), "lost"); got != Critical {
		t.Errorf("severity = %v, want Critical", got)
	}
	if findings := pvcFindings(PVCHealth{Phase: "Bound"}); len(findings) != 0 {
		t.Errorf("findings = %v, want none for a bound claim", summaries(findings))
	}
}

// A suspended or never-run CronJob is not enough signal to call broken.
func TestCronJobFindings(t *testing.T) {
	if findings := cronJobFindings(CronJobHealth{Suspended: true}, unbounded); len(findings) != 0 {
		t.Errorf("suspended CronJob produced %v, want none", summaries(findings))
	}
	if findings := cronJobFindings(CronJobHealth{}, unbounded); len(findings) != 0 {
		t.Errorf("never-run CronJob produced %v, want none", summaries(findings))
	}

	failed := cronJobFindings(CronJobHealth{MostRecentJob: &JobHealth{
		Failed: 3, BackoffLimit: 2, BackoffLimitExceeded: true}}, unbounded)
	if !hasSummaryContaining(failed, "Most recent run: BackoffLimitExceeded") {
		t.Errorf("findings = %v, want the run prefixed", summaries(failed))
	}
}

func TestContainerWaitingReasons(t *testing.T) {
	cases := map[string]struct {
		reason   string
		want     Severity
		contains string
	}{
		"crashloop":    {"CrashLoopBackOff", Critical, "CrashLoopBackOff in pod nginx"},
		"image pull":   {"ImagePullBackOff", Critical, "Image pull failure"},
		"bad image":    {"InvalidImageName", Critical, "Image pull failure"},
		"config error": {"CreateContainerConfigError", Critical, "Container config error"},
		"other":        {"ContainerCreating", Warning, "waiting: ContainerCreating"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			findings := containerFindings("nginx", ContainerDiagnostic{
				Name: "app", WaitingReason: tc.reason}, unbounded)
			if got := severityOf(t, findings, tc.contains); got != tc.want {
				t.Errorf("severity = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOOMKilledFromEitherState(t *testing.T) {
	current := containerFindings("nginx", ContainerDiagnostic{
		Name: "app", TerminatedReason: "OOMKilled"}, unbounded)
	if got := severityOf(t, current, "OOMKilled"); got != Critical {
		t.Errorf("severity = %v, want Critical", got)
	}

	// The common case: the container restarted, so the OOM is in its previous
	// state rather than its current one.
	previous := containerFindings("nginx", ContainerDiagnostic{
		Name: "app", LastTerminatedReason: "OOMKilled"}, unbounded)
	if !hasSummaryContaining(previous, "OOMKilled") {
		t.Errorf("findings = %v, want an OOMKilled finding", summaries(previous))
	}
}

// A clean exit is not a failure.
func TestCompletedContainerIsNotAFinding(t *testing.T) {
	exit := int32(0)
	findings := containerFindings("job-1", ContainerDiagnostic{
		Name: "run", TerminatedReason: "Completed", ExitCode: &exit, Ready: true}, unbounded)
	if len(findings) != 0 {
		t.Errorf("findings = %v, want none", summaries(findings))
	}
}

func TestRestartThreshold(t *testing.T) {
	below := containerFindings("nginx", ContainerDiagnostic{Name: "app", RestartCount: 4}, unbounded)
	if hasSummaryContaining(below, "restarted") {
		t.Errorf("4 restarts produced %v, want none", summaries(below))
	}
	at := containerFindings("nginx", ContainerDiagnostic{Name: "app", RestartCount: 5}, unbounded)
	if got := severityOf(t, at, "restarted 5 times"); got != Warning {
		t.Errorf("severity = %v, want Warning", got)
	}
}

// A waiting container already reports its restart count in a more specific
// finding, so repeating it adds nothing.
func TestRestartFindingSuppressedWhileWaiting(t *testing.T) {
	findings := containerFindings("nginx", ContainerDiagnostic{
		Name: "app", RestartCount: 9, WaitingReason: "CrashLoopBackOff"}, unbounded)
	if hasSummaryContaining(findings, "restarted 9 times") {
		t.Errorf("findings = %v, want no separate restart finding", summaries(findings))
	}
}

// Thresholds mirror the CPU%/MEM% colouring in kx top.
func TestUsageFindings(t *testing.T) {
	cases := []struct {
		name            string
		container       ContainerDiagnostic
		want            Severity
		contains        string
		expectNoFinding bool
	}{
		{name: "memory critical", want: Critical, contains: "OOMKill risk",
			container: ContainerDiagnostic{
				MemoryUsage: quantity("95Mi"), MemoryLimit: quantity("100Mi")}},
		{name: "memory warning", want: Warning, contains: "Memory at 80%",
			container: ContainerDiagnostic{
				MemoryUsage: quantity("80Mi"), MemoryLimit: quantity("100Mi")}},
		{name: "memory below threshold", expectNoFinding: true,
			container: ContainerDiagnostic{
				MemoryUsage: quantity("50Mi"), MemoryLimit: quantity("100Mi")}},
		{name: "cpu warning", want: Warning, contains: "likely throttling",
			container: ContainerDiagnostic{
				CPUUsage: quantity("950m"), CPULimit: quantity("1")}},
		{name: "cpu below threshold", expectNoFinding: true,
			container: ContainerDiagnostic{
				CPUUsage: quantity("500m"), CPULimit: quantity("1")}},
		// No limit and no usage are both silent: nothing to compare against is
		// not a defect.
		{name: "no limit", expectNoFinding: true,
			container: ContainerDiagnostic{MemoryUsage: quantity("500Mi")}},
		{name: "no usage", expectNoFinding: true,
			container: ContainerDiagnostic{MemoryLimit: quantity("100Mi")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := usageFindings(tc.container)
			if tc.expectNoFinding {
				if len(findings) != 0 {
					t.Errorf("findings = %v, want none", summaries(findings))
				}
				return
			}
			if got := severityOf(t, findings, tc.contains); got != tc.want {
				t.Errorf("severity = %v, want %v", got, tc.want)
			}
		})
	}
}

// CPU never reaches critical: throttling degrades performance, it doesn't take
// the container down the way a memory breach does.
func TestCPUNeverCritical(t *testing.T) {
	findings := usageFindings(ContainerDiagnostic{
		CPUUsage: quantity("2"), CPULimit: quantity("1")})
	for _, finding := range findings {
		if finding.Severity == Critical {
			t.Errorf("CPU produced a critical finding: %q", finding.Summary)
		}
	}
}

func TestPodPhaseFindings(t *testing.T) {
	unschedulable := podFindings(PodDiagnostic{
		Name: "nginx", Phase: "Pending",
		Scheduling: SchedulingInfo{Schedulable: false, Message: "0/1 nodes are available"},
	}, unbounded)
	if got := severityOf(t, unschedulable, "Unschedulable: 0/1 nodes"); got != Critical {
		t.Errorf("severity = %v, want Critical", got)
	}

	pending := podFindings(PodDiagnostic{
		Name: "nginx", Phase: "Pending", Scheduling: SchedulingInfo{Schedulable: true}}, unbounded)
	if got := severityOf(t, pending, "pending"); got != Warning {
		t.Errorf("severity = %v, want Warning", got)
	}

	failed := podFindings(PodDiagnostic{Name: "nginx", Phase: "Failed"}, unbounded)
	if got := severityOf(t, failed, "failed"); got != Critical {
		t.Errorf("severity = %v, want Critical", got)
	}
}

// A waiting container already produced a specific finding, so the generic
// containers-ready warning would be noise on top of it.
func TestReadyShortfallSuppressedWhenAContainerIsWaiting(t *testing.T) {
	findings := podFindings(PodDiagnostic{
		Name: "nginx", Phase: "Running", ReadyContainers: 0, TotalContainers: 1,
		Containers: []ContainerDiagnostic{{Name: "app", WaitingReason: "CrashLoopBackOff"}},
	}, unbounded)
	if hasSummaryContaining(findings, "0/1 containers ready") {
		t.Errorf("findings = %v, want no generic ready finding", summaries(findings))
	}

	// With no waiting container, the shortfall is worth reporting.
	notReady := podFindings(PodDiagnostic{
		Name: "nginx", Phase: "Running", ReadyContainers: 1, TotalContainers: 2,
		Containers: []ContainerDiagnostic{{Name: "app"}, {Name: "sidecar"}},
	}, unbounded)
	if got := severityOf(t, notReady, "1/2 containers ready"); got != Warning {
		t.Errorf("severity = %v, want Warning", got)
	}
}

// The message is omitted deliberately: the WARNING EVENTS section renders it in
// full, so repeating it here would bloat the summary.
func TestEventFindingsOmitTheMessage(t *testing.T) {
	findings := eventFindings([]EventSummary{{
		Reason: "BackOff", Kind: "Pod", Name: "nginx", Count: 3,
		Message: "Back-off restarting failed container",
	}})
	if len(findings) != 1 {
		t.Fatalf("findings = %v, want 1", summaries(findings))
	}
	if findings[0].Summary != "BackOff ×3 on Pod/nginx" {
		t.Errorf("summary = %q", findings[0].Summary)
	}
}

func TestFilterSeverityLines(t *testing.T) {
	raw := []string{
		"starting up", "INFO ready", "ERROR failed to connect", "",
		"WARN retrying", "done",
	}
	lines, matched := FilterSeverityLines(raw)
	if !matched {
		t.Error("matched = false, want true")
	}
	if len(lines) != 2 || !strings.Contains(lines[0], "ERROR") {
		t.Errorf("lines = %v, want the severity-matching lines", lines)
	}
}

// A failing container must always show something, even with no severity tokens.
func TestFilterSeverityLinesFallsBackToTail(t *testing.T) {
	raw := []string{"one", "two", "three", "four", "five"}
	lines, matched := FilterSeverityLines(raw)
	if matched {
		t.Error("matched = true, want false")
	}
	if len(lines) != 3 || lines[0] != "three" {
		t.Errorf("lines = %v, want the last three raw lines", lines)
	}
}

func TestFilterSeverityLinesSkipsBlanks(t *testing.T) {
	lines, _ := FilterSeverityLines([]string{"", "   ", "only"})
	if len(lines) != 1 || lines[0] != "only" {
		t.Errorf("lines = %v, want the one non-blank line", lines)
	}
}

func TestIngressFindings(t *testing.T) {
	if findings := ingressFindings(IngressHealth{}); len(findings) != 0 {
		t.Errorf("findings = %v, want none when every backend resolves", summaries(findings))
	}

	one := ingressFindings(IngressHealth{MissingBackends: []string{"api"}})
	if got := severityOf(t, one, "api"); got != Critical {
		t.Errorf("severity = %v, want Critical", got)
	}
	if !hasSummaryContaining(one, "Ingress references missing Service 'api'") {
		t.Errorf("findings = %v, want the exact missing-backend message", summaries(one))
	}

	many := ingressFindings(IngressHealth{MissingBackends: []string{"api", "web"}})
	if len(many) != 2 {
		t.Fatalf("findings = %v, want one per missing backend", summaries(many))
	}
	if !hasSummaryContaining(many, "'api'") || !hasSummaryContaining(many, "'web'") {
		t.Errorf("findings = %v, want both backends named", summaries(many))
	}
}

// crashingPod is a Deployment-shaped Data: replicas short AND a pod that says
// why. Both findings are Critical, so severity alone cannot separate them.
func brokenWorkload(pod PodDiagnostic) Data {
	return Data{
		Kind:     "Deployment",
		Replicas: &ReplicaHealth{Desired: 1, Ready: 0, Available: 0, Updated: 1},
		Pods:     []PodDiagnostic{pod},
	}
}

// The triage table shows one finding per row, so which Critical finding sorts
// first decides what a whole sweep reads like. Five differently-broken
// Deployments all reported "Only 0/1 replicas ready" — the replica shortfall
// is produced first and the sort is stable, so it won every tie and buried the
// cause one row down in the detail view.
func TestTopFindingNamesTheCauseNotTheReplicaShortfall(t *testing.T) {
	for _, testCase := range []struct {
		name string
		pod  PodDiagnostic
		want string
	}{
		{
			name: "crashloop",
			pod: PodDiagnostic{
				Name: "worker-abc", Phase: "Running", TotalContainers: 1,
				Containers: []ContainerDiagnostic{
					{Name: "worker", WaitingReason: "CrashLoopBackOff", RestartCount: 12},
				},
			},
			want: "CrashLoopBackOff",
		},
		{
			name: "image pull",
			pod: PodDiagnostic{
				Name: "api-abc", Phase: "Pending", TotalContainers: 1,
				Scheduling: SchedulingInfo{Schedulable: true},
				Containers: []ContainerDiagnostic{
					{Name: "api", WaitingReason: "ImagePullBackOff"},
				},
			},
			want: "Image pull failure",
		},
		{
			name: "oomkilled",
			pod: PodDiagnostic{
				Name: "cache-abc", Phase: "Running", TotalContainers: 1,
				Containers: []ContainerDiagnostic{
					{Name: "cache", LastTerminatedReason: "OOMKilled"},
				},
			},
			want: "OOMKilled",
		},
		{
			name: "config error",
			pod: PodDiagnostic{
				Name: "queue-abc", Phase: "Pending", TotalContainers: 1,
				Scheduling: SchedulingInfo{Schedulable: true},
				Containers: []ContainerDiagnostic{
					{Name: "queue", WaitingReason: "CreateContainerConfigError",
						WaitingMessage: "secret \"queue-creds\" not found"},
				},
			},
			want: "Container config error",
		},
		{
			name: "terminated non-zero",
			pod: PodDiagnostic{
				Name: "batch-abc", Phase: "Running", TotalContainers: 1,
				Containers: []ContainerDiagnostic{
					{Name: "batch", TerminatedReason: "Error", ExitCode: exitCode(137)},
				},
			},
			want: "terminated: Error (exit 137)",
		},
		{
			name: "unschedulable",
			pod: PodDiagnostic{
				Name: "report-abc", Phase: "Pending", TotalContainers: 1,
				Scheduling: SchedulingInfo{Schedulable: false, Reason: "Unschedulable",
					Message: "0/1 nodes are available: insufficient cpu"},
			},
			want: "Unschedulable: 0/1 nodes are available",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			report := BuildReport(brokenWorkload(testCase.pod))
			if len(report.Findings) == 0 {
				t.Fatal("no findings")
			}
			top := report.Findings[0].Summary
			if !strings.Contains(top, testCase.want) {
				t.Errorf("top finding = %q, want it to name %q\nall: %v",
					top, testCase.want, summaries(report.Findings))
			}
		})
	}
}

// Rank breaks ties within a severity; it never reaches across one. A Critical
// aggregate outranks a Warning cause, because the reader wants the worst thing
// first and only then the most specific.
func TestSeverityStillOutranksSpecificity(t *testing.T) {
	report := BuildReport(Data{
		Kind:     "Deployment",
		Replicas: &ReplicaHealth{Desired: 1, Ready: 0, Available: 0, Updated: 1},
		Pods: []PodDiagnostic{{
			Name: "web-abc", Phase: "Running", ReadyContainers: 0, TotalContainers: 1,
			Containers: []ContainerDiagnostic{{Name: "web", RestartCount: 9}},
		}},
	})
	if report.Findings[0].Severity != Critical {
		t.Fatalf("top finding = %v, want the Critical one first: %v",
			report.Findings[0], summaries(report.Findings))
	}
	if !strings.Contains(report.Findings[0].Summary, "replicas ready") {
		t.Errorf("top finding = %q, want the only Critical finding",
			report.Findings[0].Summary)
	}
}

// A warning event is the weakest headline there is: the WARNING EVENTS section
// already prints it in full, and it usually restates a symptom a pod-level
// finding has already explained.
//
// Pinned against an *aggregate* rather than a cause. BuildReport appends
// events last, so a stable sort already puts them behind every equally severe
// finding produced before them — an event outranking a cause is unreachable,
// and a test asserting it passes no matter what rank events carry. Ranking
// below an aggregate is the one thing the Event rank actually decides.
func TestWarningEventsRankBelowAggregates(t *testing.T) {
	report := BuildReport(Data{
		Kind:     "Deployment",
		Replicas: &ReplicaHealth{Desired: 2, Ready: 1, Available: 1, Updated: 2},
		WarningEvents: []EventSummary{
			{Reason: "BackOff", Count: 300, Kind: "Pod", Name: "billing-abc"},
		},
	})
	if len(report.Findings) < 2 {
		t.Fatalf("want both findings, got %v", summaries(report.Findings))
	}
	if !strings.Contains(report.Findings[0].Summary, "replicas ready") {
		t.Errorf("top finding = %q, want the aggregate above the raw event\nall: %v",
			report.Findings[0].Summary, summaries(report.Findings))
	}
	last := report.Findings[len(report.Findings)-1]
	if !strings.Contains(last.Summary, "BackOff") {
		t.Errorf("last finding = %q, want the raw event\nall: %v",
			last.Summary, summaries(report.Findings))
	}
}

// Within one rank the order findings were produced in still survives, so a
// pod's containers keep reporting in the order they are declared.
//
// This pins the observable order, not the choice of sort.SliceStable over
// sort.Slice. Nothing can pin that here: every finding in this fixture
// compares equal, so the comparator never returns true, and Go's pdqsort
// recognises an already-ordered run and leaves it alone whatever the size.
// SliceStable stays because the guarantee is what the code means to rely on —
// but a mutation to sort.Slice survives this test, and no test in this package
// would catch it.
func TestOrderWithinARankIsStable(t *testing.T) {
	const count = 24
	containers := make([]ContainerDiagnostic, 0, count)
	want := make([]string, 0, count)
	for i := range count {
		name := fmt.Sprintf("c%02d", i)
		containers = append(containers, ContainerDiagnostic{
			Name: name, WaitingReason: "CreateContainerConfigError",
			WaitingMessage: "secret " + name + " not found",
		})
		want = append(want, name)
	}

	report := BuildReport(Data{
		Kind: "Pod", Name: "multi",
		Pods: []PodDiagnostic{{
			Name: "multi", Phase: "Running", TotalContainers: count,
			Containers: containers,
		}},
	})
	if len(report.Findings) != count {
		t.Fatalf("got %d findings, want %d", len(report.Findings), count)
	}
	for i, finding := range report.Findings {
		if !strings.Contains(finding.Summary, want[i]) {
			t.Fatalf("finding %d = %q, want it to name %q — declaration order was not preserved",
				i, finding.Summary, want[i])
		}
	}
}

func exitCode(value int32) *int32 { return &value }

// A Node that has stopped reporting is the single most consequential thing a
// cluster can be told about, and kx had nothing to say about it: kx diag on a
// Node index answered "diagnostic is not supported for 'Node'".
func TestNodeNotReadyIsCritical(t *testing.T) {
	report := BuildReport(Data{
		Kind: kinds.Node, Name: "node-a",
		Node: &NodeHealth{Conditions: []NodeCondition{
			{Type: "Ready", Status: "False", Reason: "KubeletNotReady",
				Message: "container runtime is down"},
		}},
	})
	if report.Verdict != Critical {
		t.Errorf("verdict = %v, want Critical", report.Verdict)
	}
	if !hasSummaryContaining(report.Findings, "Not ready") {
		t.Errorf("findings = %v, want a not-ready finding", summaries(report.Findings))
	}
	if !hasSummaryContaining(report.Findings, "container runtime is down") {
		t.Errorf("findings = %v, want the condition's own message", summaries(report.Findings))
	}
}

// Unknown is not False. A kubelet that stopped reporting leaves Ready at
// Unknown, and saying "not ready" for it would claim knowledge kx does not
// have — the node may be running everything perfectly behind a dead kubelet.
func TestNodeReadyUnknownIsReportedAsUnknown(t *testing.T) {
	report := BuildReport(Data{
		Kind: kinds.Node, Name: "node-a",
		Node: &NodeHealth{Conditions: []NodeCondition{
			{Type: "Ready", Status: "Unknown", Reason: "NodeStatusUnknown",
				Message: "kubelet stopped posting node status"},
		}},
	})
	if report.Verdict != Critical {
		t.Errorf("verdict = %v, want Critical", report.Verdict)
	}
	if !hasSummaryContaining(report.Findings, "status unknown") {
		t.Errorf("findings = %v, want an unknown-status finding, not a not-ready one",
			summaries(report.Findings))
	}
}

// The pressure conditions are the ones that evict pods, and they are inverted
// relative to Ready: True is the bad state.
func TestNodePressureConditions(t *testing.T) {
	for _, condition := range []struct{ name, want string }{
		{"MemoryPressure", "Under memory pressure"},
		{"DiskPressure", "Under disk pressure"},
		{"PIDPressure", "Under PID pressure"},
		{"NetworkUnavailable", "Network unavailable"},
	} {
		report := BuildReport(Data{
			Kind: kinds.Node, Name: "node-a",
			Node: &NodeHealth{Conditions: []NodeCondition{
				{Type: "Ready", Status: "True"},
				{Type: condition.name, Status: "True", Reason: "Pressure"},
			}},
		})
		if report.Verdict != Critical {
			t.Errorf("%s: verdict = %v, want Critical", condition.name, report.Verdict)
		}
		if !hasSummaryContaining(report.Findings, condition.want) {
			t.Errorf("%s: findings = %v, want %q",
				condition.name, summaries(report.Findings), condition.want)
		}
	}
}

// A pressure condition at False is the normal state and must produce nothing;
// a Ready node with nothing else wrong is healthy.
func TestHealthyNodeHasNoFindings(t *testing.T) {
	report := BuildReport(Data{
		Kind: kinds.Node, Name: "node-a",
		Node: &NodeHealth{Conditions: []NodeCondition{
			{Type: "Ready", Status: "True"},
			{Type: "MemoryPressure", Status: "False"},
			{Type: "DiskPressure", Status: "False"},
			{Type: "PIDPressure", Status: "False"},
		}, Pods: PodPhaseCounts{Total: 12, Running: 12}},
	})
	if report.Verdict != OK {
		t.Errorf("verdict = %v, want OK: %v", report.Verdict, summaries(report.Findings))
	}
	if len(report.Findings) != 0 {
		t.Errorf("findings = %v, want none", summaries(report.Findings))
	}
}

// Cordoned is a warning, not a critical: it is usually deliberate, and it is
// exactly what kx cordon just did.
func TestCordonedNodeIsAWarning(t *testing.T) {
	report := BuildReport(Data{
		Kind: kinds.Node, Name: "node-a",
		Node: &NodeHealth{
			Conditions:    []NodeCondition{{Type: "Ready", Status: "True"}},
			Unschedulable: true,
		},
	})
	if report.Verdict != Warning {
		t.Errorf("verdict = %v, want Warning", report.Verdict)
	}
	if !hasSummaryContaining(report.Findings, "Cordoned") {
		t.Errorf("findings = %v, want a cordoned finding", summaries(report.Findings))
	}
}

// Pods that are not running on an otherwise healthy node are worth a line, and
// it says where to look rather than listing hundreds of pods.
func TestNodePodRollupReportsWhatIsNotRunning(t *testing.T) {
	report := BuildReport(Data{
		Kind: kinds.Node, Name: "node-a",
		Node: &NodeHealth{
			Conditions: []NodeCondition{{Type: "Ready", Status: "True"}},
			Pods:       PodPhaseCounts{Total: 20, Running: 17, Pending: 2, Unknown: 1},
		},
	})
	if !hasSummaryContaining(report.Findings, "3/20 pods not running") {
		t.Errorf("findings = %v, want a pod rollup", summaries(report.Findings))
	}
	if severityOf(t, report.Findings, "pods not running") != Warning {
		t.Error("the pod rollup should be a warning, not a critical")
	}
}

// The rollup ranks below the conditions: a node reporting both is broken for
// the reason the condition names, and the pods are downstream of it.
func TestNodeConditionOutranksThePodRollup(t *testing.T) {
	report := BuildReport(Data{
		Kind: kinds.Node, Name: "node-a",
		Node: &NodeHealth{
			Conditions: []NodeCondition{{Type: "Ready", Status: "True"},
				{Type: "MemoryPressure", Status: "True"}},
			Pods: PodPhaseCounts{Total: 20, Running: 17, Pending: 3},
		},
	})
	if !strings.Contains(report.Findings[0].Summary, "memory pressure") {
		t.Errorf("top finding = %q, want the condition above the rollup: %v",
			report.Findings[0].Summary, summaries(report.Findings))
	}
}

func TestNodeIsASupportedKind(t *testing.T) {
	if !SupportedKinds.Has(kinds.Node) {
		t.Error("Node is not a supported diagnostic kind")
	}
}

// A Job's pod stays on the node after it completes. Counting those as "not
// running" would report every node that has ever run a CronJob as degraded.
func TestNodeRollupIgnoresCompletedPods(t *testing.T) {
	report := BuildReport(Data{
		Kind: kinds.Node, Name: "node-a",
		Node: &NodeHealth{
			Conditions: []NodeCondition{{Type: "Ready", Status: "True"}},
			Pods:       PodPhaseCounts{Total: 20, Running: 12, Succeeded: 8},
		},
	})
	if report.Verdict != OK {
		t.Errorf("verdict = %v, want OK: %v", report.Verdict, summaries(report.Findings))
	}
	if hasSummaryContaining(report.Findings, "not running") {
		t.Errorf("findings = %v, want no rollup for completed pods", summaries(report.Findings))
	}
}

// A pod that failed leaves its object on the node until the terminated-pod GC
// threshold — 12500 by default — so counting one as "not running" left a node
// permanently degraded by an eviction that happened days ago, and (once
// --fail-on shipped) failed a CI gate forever on a healthy cluster.
//
// Same reasoning as completed pods, which the code already excluded. The pod
// is still reported where it belongs: against the workload that owns it.
func TestNodeRollupIgnoresPodsThatAlreadyFailed(t *testing.T) {
	report := BuildReport(Data{
		Kind: kinds.Node, Name: "node-a",
		Node: &NodeHealth{
			Conditions: []NodeCondition{{Type: "Ready", Status: "True"}},
			Pods:       PodPhaseCounts{Total: 40, Running: 39, Failed: 1},
		},
	})
	if hasSummaryContaining(report.Findings, "pods not running") {
		t.Errorf("findings = %v, want no rollup for a node whose only non-running pod failed",
			summaries(report.Findings))
	}
	if report.Verdict != OK {
		t.Errorf("verdict = %v, want OK — a failed pod is not a sick node", report.Verdict)
	}
}

// Pending and Unknown still count: those are pods the node has not placed or
// is not reporting on, which is a fact about the node right now.
func TestNodeRollupStillCountsPendingAndUnknown(t *testing.T) {
	report := BuildReport(Data{
		Kind: kinds.Node, Name: "node-a",
		Node: &NodeHealth{
			Conditions: []NodeCondition{{Type: "Ready", Status: "True"}},
			Pods:       PodPhaseCounts{Total: 45, Running: 37, Pending: 2, Unknown: 1, Failed: 5},
		},
	})
	if !hasSummaryContaining(report.Findings, "3/40 pods not running") {
		t.Errorf("findings = %v, want the pending and unknown pods counted and the failed ones not",
			summaries(report.Findings))
	}
}

// The ratio's two halves have to count the same pods.
//
// Stalled excludes terminated pods on purpose — see PodPhaseCounts.Stalled —
// so the denominator has to exclude them too. The live shape this was found
// in: a node with 23 running, 5 failed and 1 pending reported "1/29 pods not
// running" while kubectl showed 6 of the 29 not running. 24 pods are still
// expected to be running there and 23 of them are, which is what "1/24" says.
func TestNodeRollupDenominatorExcludesTerminatedPods(t *testing.T) {
	report := BuildReport(Data{
		Kind: kinds.Node, Name: "node-a",
		Node: &NodeHealth{
			Conditions: []NodeCondition{{Type: "Ready", Status: "True"}},
			Pods:       PodPhaseCounts{Total: 29, Running: 23, Failed: 5, Pending: 1},
		},
	})
	if !hasSummaryContaining(report.Findings, "1/24 pods not running") {
		t.Errorf("findings = %v,\n  want the terminated pods out of the denominator",
			summaries(report.Findings))
	}
	if hasSummaryContaining(report.Findings, "1/29") {
		t.Errorf("findings = %v,\n  want the denominator to exclude the 5 failed pods",
			summaries(report.Findings))
	}
}

// Active is Stalled's denominator, so it is the same question asked of the
// whole tally: which pods is this node still supposed to be running?
func TestActiveCountsOnlyPodsStillExpectedToRun(t *testing.T) {
	counts := PodPhaseCounts{Total: 29, Running: 23, Failed: 5, Pending: 1}
	if got := counts.Active(); got != 24 {
		t.Errorf("Active() = %d, want 24 — 29 pods less the 5 that terminated", got)
	}
	if got := counts.Stalled(); got != 1 {
		t.Errorf("Stalled() = %d, want 1", got)
	}
}

// The window that a report was gathered under, and two ages either side of it.
var (
	windowStart = time.Now().Add(-24 * time.Hour)
	recently    = time.Now().Add(-10 * time.Minute)
	longAgo     = time.Now().Add(-21 * 24 * time.Hour)
)

// An OOMKill in the *previous* state is history: the container has been
// running ever since, which is the evidence that the problem passed. Reported
// forever, it held a healthy workload at critical — the complaint that opened
// #337, in a signal the event window doesn't touch.
func TestStaleOOMKillIsNotReported(t *testing.T) {
	findings := containerFindings("nginx", ContainerDiagnostic{
		Name: "app", Ready: true, State: "Running",
		LastTerminatedReason: "OOMKilled", LastTerminatedAt: longAgo,
	}, windowStart)
	if hasSummaryContaining(findings, "OOMKilled") {
		t.Errorf("findings = %v, want no OOMKilled from three weeks ago", summaries(findings))
	}
}

func TestRecentOOMKillIsStillReported(t *testing.T) {
	findings := containerFindings("nginx", ContainerDiagnostic{
		Name: "app", Ready: true, State: "Running",
		LastTerminatedReason: "OOMKilled", LastTerminatedAt: recently,
	}, windowStart)
	if !hasSummaryContaining(findings, "OOMKilled") {
		t.Errorf("findings = %v, want the OOMKilled from ten minutes ago", summaries(findings))
	}
}

// Current state is never bounded: a container that is terminated *now* is
// broken now, however long ago it died.
func TestCurrentOOMKillIsReportedWhateverItsAge(t *testing.T) {
	findings := containerFindings("nginx", ContainerDiagnostic{
		Name: "app", State: "Terminated", TerminatedReason: "OOMKilled",
		LastTerminatedAt: longAgo,
	}, windowStart)
	if !hasSummaryContaining(findings, "OOMKilled") {
		t.Errorf("findings = %v, want the current OOMKilled", summaries(findings))
	}
}

// The restart count is cumulative over the pod's whole life, so it says
// nothing about when the thrashing happened. The last termination does.
func TestStaleRestartsAreNotReported(t *testing.T) {
	findings := containerFindings("nginx", ContainerDiagnostic{
		Name: "app", Ready: true, State: "Running",
		RestartCount: 21, LastTerminatedAt: longAgo,
	}, windowStart)
	if hasSummaryContaining(findings, "restarted") {
		t.Errorf("findings = %v, want no restart finding for a pod that settled", summaries(findings))
	}
}

func TestRecentRestartsAreStillReported(t *testing.T) {
	findings := containerFindings("nginx", ContainerDiagnostic{
		Name: "app", Ready: true, State: "Running",
		RestartCount: 21, LastTerminatedAt: recently,
	}, windowStart)
	if !hasSummaryContaining(findings, "restarted 21 times") {
		t.Errorf("findings = %v, want the restart finding", summaries(findings))
	}
}

// A container thrashing right now is in CrashLoopBackOff, which is current
// state — the window must not reach it even though its last exit is old.
func TestCrashLoopBackOffIsReportedWhateverItsAge(t *testing.T) {
	findings := containerFindings("nginx", ContainerDiagnostic{
		Name: "app", WaitingReason: "CrashLoopBackOff",
		RestartCount: 21, LastTerminatedAt: longAgo,
	}, windowStart)
	if !hasSummaryContaining(findings, "CrashLoopBackOff") {
		t.Errorf("findings = %v, want the CrashLoopBackOff", summaries(findings))
	}
}

// Nothing datable, nothing to prove stale — the same rule an undated event
// gets. Suppressing on a missing field would hide a live failure.
func TestUndatedHistoryIsStillReported(t *testing.T) {
	findings := containerFindings("nginx", ContainerDiagnostic{
		Name: "app", Ready: true, State: "Running",
		RestartCount: 21, LastTerminatedReason: "OOMKilled",
	}, windowStart)
	for _, want := range []string{"OOMKilled", "restarted 21 times"} {
		if !hasSummaryContaining(findings, want) {
			t.Errorf("findings = %v, want %q kept when nothing dates it", summaries(findings), want)
		}
	}
}

// An unbounded report — --since 0, or a caller with no window — is the zero
// value, and must behave exactly as it did before the window existed.
func TestWithoutAWindowEveryHistoricalFindingSurvives(t *testing.T) {
	findings := containerFindings("nginx", ContainerDiagnostic{
		Name: "app", Ready: true, State: "Running", RestartCount: 21,
		LastTerminatedReason: "OOMKilled", LastTerminatedAt: longAgo,
	}, time.Time{})
	for _, want := range []string{"OOMKilled", "restarted 21 times"} {
		if !hasSummaryContaining(findings, want) {
			t.Errorf("findings = %v, want %q with no window set", summaries(findings), want)
		}
	}
}

func TestStaleJobFailureIsNotReported(t *testing.T) {
	job := JobHealth{Failed: 6, BackoffLimit: 5, BackoffLimitExceeded: true, FailedAt: longAgo}
	if findings := jobFindings(job, windowStart); len(findings) != 0 {
		t.Errorf("findings = %v, want none for a run that failed three weeks ago",
			summaries(findings))
	}
}

func TestRecentJobFailureIsStillReported(t *testing.T) {
	job := JobHealth{Failed: 6, BackoffLimit: 5, BackoffLimitExceeded: true, FailedAt: recently}
	if !hasSummaryContaining(jobFindings(job, windowStart), "BackoffLimitExceeded") {
		t.Error("a run that failed ten minutes ago was dropped")
	}
}

// A CronJob rolls up its most recent run, so it inherits the same rule — and
// with it the caveat that a schedule longer than the window needs --since
// widened to see the last failure.
func TestStaleCronJobRunIsNotReported(t *testing.T) {
	cronJob := CronJobHealth{MostRecentJob: &JobHealth{
		Failed: 1, BackoffLimit: 0, BackoffLimitExceeded: true, FailedAt: longAgo,
	}}
	if findings := cronJobFindings(cronJob, windowStart); len(findings) != 0 {
		t.Errorf("findings = %v, want none for a run from three weeks ago", summaries(findings))
	}
}

func TestRecentCronJobRunIsStillReported(t *testing.T) {
	cronJob := CronJobHealth{MostRecentJob: &JobHealth{
		Failed: 1, BackoffLimit: 0, BackoffLimitExceeded: true, FailedAt: recently,
	}}
	if !hasSummaryContaining(cronJobFindings(cronJob, windowStart), "Most recent run") {
		t.Error("a run that failed ten minutes ago was dropped")
	}
}

// BuildReport is the only caller that knows the window the data was gathered
// under, so the cutoff has to travel on the Data rather than be recomputed.
func TestBuildReportAppliesTheDataWindowToHistory(t *testing.T) {
	data := Data{
		Kind: kinds.Deployment, Name: "web", Since: windowStart,
		Pods: []PodDiagnostic{{
			Name: "web-1", Phase: "Running", ReadyContainers: 1, TotalContainers: 1,
			Containers: []ContainerDiagnostic{{
				Name: "app", Ready: true, State: "Running", RestartCount: 21,
				LastTerminatedReason: "OOMKilled", LastTerminatedAt: longAgo,
			}},
		}},
	}
	report := BuildReport(data)
	if report.Verdict != OK {
		t.Errorf("verdict = %v (%v), want healthy — every signal is three weeks old",
			report.Verdict, summaries(report.Findings))
	}
}
