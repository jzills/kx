package diagnostics

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
)

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
		if findings := jobFindings(job); len(findings) != 0 {
			t.Errorf("job %+v produced %v, want none", job, summaries(findings))
		}
	}

	failed := jobFindings(JobHealth{Failed: 6, BackoffLimit: 5, BackoffLimitExceeded: true})
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
	if findings := cronJobFindings(CronJobHealth{Suspended: true}); len(findings) != 0 {
		t.Errorf("suspended CronJob produced %v, want none", summaries(findings))
	}
	if findings := cronJobFindings(CronJobHealth{}); len(findings) != 0 {
		t.Errorf("never-run CronJob produced %v, want none", summaries(findings))
	}

	failed := cronJobFindings(CronJobHealth{MostRecentJob: &JobHealth{
		Failed: 3, BackoffLimit: 2, BackoffLimitExceeded: true}})
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
				Name: "app", WaitingReason: tc.reason})
			if got := severityOf(t, findings, tc.contains); got != tc.want {
				t.Errorf("severity = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOOMKilledFromEitherState(t *testing.T) {
	current := containerFindings("nginx", ContainerDiagnostic{
		Name: "app", TerminatedReason: "OOMKilled"})
	if got := severityOf(t, current, "OOMKilled"); got != Critical {
		t.Errorf("severity = %v, want Critical", got)
	}

	// The common case: the container restarted, so the OOM is in its previous
	// state rather than its current one.
	previous := containerFindings("nginx", ContainerDiagnostic{
		Name: "app", LastTerminatedReason: "OOMKilled"})
	if !hasSummaryContaining(previous, "OOMKilled") {
		t.Errorf("findings = %v, want an OOMKilled finding", summaries(previous))
	}
}

// A clean exit is not a failure.
func TestCompletedContainerIsNotAFinding(t *testing.T) {
	exit := int32(0)
	findings := containerFindings("job-1", ContainerDiagnostic{
		Name: "run", TerminatedReason: "Completed", ExitCode: &exit, Ready: true})
	if len(findings) != 0 {
		t.Errorf("findings = %v, want none", summaries(findings))
	}
}

func TestRestartThreshold(t *testing.T) {
	below := containerFindings("nginx", ContainerDiagnostic{Name: "app", RestartCount: 4})
	if hasSummaryContaining(below, "restarted") {
		t.Errorf("4 restarts produced %v, want none", summaries(below))
	}
	at := containerFindings("nginx", ContainerDiagnostic{Name: "app", RestartCount: 5})
	if got := severityOf(t, at, "restarted 5 times"); got != Warning {
		t.Errorf("severity = %v, want Warning", got)
	}
}

// A waiting container already reports its restart count in a more specific
// finding, so repeating it adds nothing.
func TestRestartFindingSuppressedWhileWaiting(t *testing.T) {
	findings := containerFindings("nginx", ContainerDiagnostic{
		Name: "app", RestartCount: 9, WaitingReason: "CrashLoopBackOff"})
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
	})
	if got := severityOf(t, unschedulable, "unschedulable: 0/1 nodes"); got != Critical {
		t.Errorf("severity = %v, want Critical", got)
	}

	pending := podFindings(PodDiagnostic{
		Name: "nginx", Phase: "Pending", Scheduling: SchedulingInfo{Schedulable: true}})
	if got := severityOf(t, pending, "pending"); got != Warning {
		t.Errorf("severity = %v, want Warning", got)
	}

	failed := podFindings(PodDiagnostic{Name: "nginx", Phase: "Failed"})
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
	})
	if hasSummaryContaining(findings, "0/1 containers ready") {
		t.Errorf("findings = %v, want no generic ready finding", summaries(findings))
	}

	// With no waiting container, the shortfall is worth reporting.
	notReady := podFindings(PodDiagnostic{
		Name: "nginx", Phase: "Running", ReadyContainers: 1, TotalContainers: 2,
		Containers: []ContainerDiagnostic{{Name: "app"}, {Name: "sidecar"}},
	})
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
