package diagnostics

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/jzills/kx/internal/kinds"
)

// SupportedKinds are the kinds kx diag can analyse.
var SupportedKinds = kinds.Set{
	// In the order kx diag's own help names them, since the same list is now
	// printed by the error a wrong kind gets.
	kinds.Deployment,
	kinds.StatefulSet,
	kinds.DaemonSet,
	kinds.Job,
	kinds.CronJob,
	kinds.Service,
	kinds.PersistentVolumeClaim,
	kinds.Ingress,
	kinds.Pod,
	kinds.Node,
}

const restartWarnThreshold = 5

// Usage thresholds, as percentages so the comparison is integer arithmetic.
// These mirror the CPU%/MEM% colouring in kx top: a red 94% there means what a
// critical finding means here. Kept in sync by hand.
const (
	memoryWarnPct     = 75
	memoryCriticalPct = 90
	// CPU never reaches critical: throttling degrades performance, it doesn't
	// take the container down the way a memory limit breach does.
	cpuWarnPct = 90
)

var imagePullReasons = map[string]bool{
	"ImagePullBackOff": true, "ErrImagePull": true, "InvalidImageName": true,
}

var configErrorReasons = map[string]bool{
	"CreateContainerConfigError": true, "CreateContainerError": true,
}

// BuildReport distils gathered data into findings and an overall verdict.
func BuildReport(data Data) Report {
	var findings []Finding

	if data.Node != nil {
		findings = append(findings, nodeFindings(*data.Node)...)
	}
	if data.Replicas != nil {
		findings = append(findings, replicaFindings(*data.Replicas)...)
	}
	if data.Job != nil {
		findings = append(findings, jobFindings(*data.Job, data.Since)...)
	}
	if data.Service != nil {
		findings = append(findings, serviceFindings(*data.Service)...)
	}
	if data.PVC != nil {
		findings = append(findings, pvcFindings(*data.PVC)...)
	}
	if data.CronJob != nil {
		findings = append(findings, cronJobFindings(*data.CronJob, data.Since)...)
	}
	if data.Ingress != nil {
		findings = append(findings, ingressFindings(*data.Ingress)...)
	}
	for _, pod := range data.Pods {
		findings = append(findings, podFindings(pod, data.Since)...)
	}
	findings = append(findings, eventFindings(data.WarningEvents)...)

	// Highest severity first, then most specific first, then stable so the
	// order findings were produced in survives.
	//
	// Rank breaks ties within a severity and never reaches across one: the
	// reader wants the worst thing first, and only then the most specific of
	// the equally bad. Without it the aggregate findings won every tie by
	// being produced first, and a sweep of five differently-broken Deployments
	// read "Only 0/N replicas ready" five times over.
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return findings[i].Severity > findings[j].Severity
		}
		return findings[i].Rank < findings[j].Rank
	})

	verdict := OK
	for _, finding := range findings {
		if finding.Severity > verdict {
			verdict = finding.Severity
		}
	}

	return Report{
		Window:        data.Window,
		Kind:          data.Kind,
		Name:          data.Name,
		Namespace:     data.Namespace,
		Verdict:       verdict,
		Findings:      findings,
		Pods:          data.Pods,
		WarningEvents: data.WarningEvents,
	}
}

// pressureConditions are the Node conditions whose True is the bad state,
// inverted relative to Ready. Each evicts or blocks pods, so each is critical.
var pressureConditions = map[string]string{
	"MemoryPressure":     "Under memory pressure",
	"DiskPressure":       "Under disk pressure",
	"PIDPressure":        "Under PID pressure",
	"NetworkUnavailable": "Network unavailable",
}

// nodeFindings reads a Node's conditions, whether it is cordoned, and what is
// scheduled on it.
//
// Ready is tri-state and the three states mean different things. False is the
// kubelet reporting that it is not ready. Unknown is the kubelet not reporting
// at all, which is not the same claim: the node may be running everything on it
// perfectly behind a kubelet that has stopped talking, and calling that "not
// ready" would assert something kx cannot see. Both are critical; they are
// worded apart.
func nodeFindings(node NodeHealth) []Finding {
	var findings []Finding

	for _, condition := range node.Conditions {
		if condition.Type == "Ready" {
			switch condition.Status {
			case "False":
				findings = append(findings, finding(Critical, Cause,
					"Not ready: "+conditionDetail(condition)))
			case "Unknown":
				findings = append(findings, finding(Critical, Cause,
					"Node status unknown: "+conditionDetail(condition)))
			}
			continue
		}
		if label, pressure := pressureConditions[condition.Type]; pressure && condition.Status == "True" {
			findings = append(findings, finding(Critical, Cause,
				label+": "+conditionDetail(condition)))
		}
	}

	// Warning, not critical, and deliberately so: a cordoned node is usually
	// cordoned on purpose, and it is exactly what kx cordon just did.
	if node.Unschedulable {
		findings = append(findings, finding(Warning, Cause,
			"Cordoned: no new pods will be scheduled here"))
	}

	// An aggregate, and ranked as one — a node reporting both a condition and
	// stalled pods is broken for the reason the condition names, and the pods
	// are downstream of it. The count points at kubectl rather than listing
	// hundreds of pods that would not fit a diagnosis.
	//
	// Against Active, not Total: the two halves of the ratio have to count the
	// same pods, and Stalled leaves the terminated ones out.
	if stalled := node.Pods.Stalled(); stalled > 0 {
		findings = append(findings, finding(Warning, Aggregate, fmt.Sprintf(
			"%d/%d pods not running (%s)", stalled, node.Pods.Active(), phaseBreakdown(node.Pods))))
	}
	return findings
}

// conditionDetail prefers the condition's message, which names the actual
// problem, and falls back to its reason. Neither is guaranteed to be set.
func conditionDetail(condition NodeCondition) string {
	if condition.Message != "" {
		return condition.Message
	}
	if condition.Reason != "" {
		return condition.Reason
	}
	return condition.Status
}

// phaseBreakdown names the phases the stalled pods are actually in, so the
// count is actionable rather than merely alarming. Failed is absent because
// Stalled no longer counts it — see PodPhaseCounts.Stalled.
func phaseBreakdown(counts PodPhaseCounts) string {
	var parts []string
	for _, phase := range []struct {
		label string
		count int
	}{
		{"pending", counts.Pending},
		{"unknown", counts.Unknown},
	} {
		if phase.count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", phase.count, phase.label))
		}
	}
	if len(parts) == 0 {
		return "phase not reported"
	}
	return strings.Join(parts, ", ")
}

func replicaFindings(replicas ReplicaHealth) []Finding {
	var findings []Finding
	if replicas.Generation != nil && replicas.ObservedGeneration != nil &&
		*replicas.ObservedGeneration < *replicas.Generation {
		findings = append(findings, finding(Critical, Cause, fmt.Sprintf(
			"Rollout stalled: observed generation %d behind spec generation %d",
			*replicas.ObservedGeneration, *replicas.Generation)))
	}
	if replicas.Ready < replicas.Desired {
		severity := Warning
		if replicas.Ready == 0 && replicas.Desired > 0 {
			severity = Critical
		}
		findings = append(findings, finding(severity, Aggregate, fmt.Sprintf(
			"Only %d/%d replicas ready", replicas.Ready, replicas.Desired)))
	}
	if replicas.Available < replicas.Desired {
		findings = append(findings, finding(Warning, Aggregate, fmt.Sprintf(
			"%d/%d replicas available", replicas.Available, replicas.Desired)))
	}
	if replicas.Updated < replicas.Desired {
		findings = append(findings, finding(Warning, Aggregate, fmt.Sprintf(
			"Rollout in progress: %d/%d replicas updated", replicas.Updated, replicas.Desired)))
	}
	return findings
}

// jobFindings treats suspended, active and successfully completed Jobs as OK —
// the same treatment a Deployment scaled to zero already gets. Trouble in the
// Job's own pods surfaces separately through podFindings.
func jobFindings(job JobHealth, since time.Time) []Finding {
	// A run that failed before the window opened is not this report's news.
	//
	// The failure is not lost with the line: the run's failed pods hang off
	// the same Data, and a pod in Failed phase is present state, so it is
	// reported however long ago it died. A CronJob whose last run failed
	// weeks ago still reads critical — it loses this rollup, not its
	// diagnosis. Only where the pods are gone too (ttlSecondsAfterFinished,
	// a zero history limit) does the run go quiet, and --since widens the
	// window for that.
	if outsideWindow(job.FailedAt, since) {
		return nil
	}
	var findings []Finding
	if job.BackoffLimitExceeded {
		findings = append(findings, dated(Critical, Cause, job.FailedAt, fmt.Sprintf(
			"BackoffLimitExceeded (%d/%d failed)", job.Failed, job.BackoffLimit)))
	}
	if job.DeadlineExceeded {
		findings = append(findings, dated(Critical, Cause, job.FailedAt, "DeadlineExceeded"))
	}
	return findings
}

// serviceFindings treats a missing selector as a legitimate configuration
// (ExternalName, headless, manually managed Endpoints), not a defect.
func serviceFindings(service ServiceHealth) []Finding {
	if !service.HasSelector {
		return nil
	}
	total := service.ReadyAddresses + service.NotReadyAddresses
	switch {
	case total == 0:
		return []Finding{finding(Critical, Cause, "No endpoints: no pods match the selector")}
	case service.ReadyAddresses == 0:
		return []Finding{finding(Critical, Aggregate, fmt.Sprintf(
			"%d endpoint(s) not ready, 0 ready", service.NotReadyAddresses))}
	case service.NotReadyAddresses > 0:
		return []Finding{finding(Warning, Aggregate, fmt.Sprintf(
			"%d/%d endpoints ready", service.ReadyAddresses, total))}
	}
	return nil
}

// cronJobFindings rolls up the most recent run, reusing jobFindings so a
// CronJob whose last run hit BackoffLimitExceeded reads the same way a
// standalone failed Job does. Suspended and never-run are both OK — not enough
// signal to call a fresh or paused CronJob broken.
func cronJobFindings(cronJob CronJobHealth, since time.Time) []Finding {
	if cronJob.Suspended || cronJob.MostRecentJob == nil {
		return nil
	}
	var findings []Finding
	for _, run := range jobFindings(*cronJob.MostRecentJob, since) {
		findings = append(findings, dated(
			run.Severity, run.Rank, run.At, "Most recent run: "+run.Summary,
		))
	}
	return findings
}

// pvcFindings flags Pending immediately rather than after a duration, mirroring
// the unconditional "Pod pending" finding.
func pvcFindings(pvc PVCHealth) []Finding {
	switch pvc.Phase {
	case "Pending":
		return []Finding{finding(Warning, Cause, "PersistentVolumeClaim pending")}
	case "Lost":
		return []Finding{finding(Critical, Cause,
			"PersistentVolumeClaim lost: backing volume no longer available")}
	}
	return nil
}

// ingressFindings reports one Critical finding per backend Service the
// Ingress references but which does not exist. No loadBalancer/address
// check — see IngressHealth's doc comment.
func ingressFindings(ingress IngressHealth) []Finding {
	findings := make([]Finding, 0, len(ingress.MissingBackends))
	for _, name := range ingress.MissingBackends {
		findings = append(findings, finding(Critical, Cause, fmt.Sprintf(
			"Ingress references missing Service '%s'", name)))
	}
	return findings
}

func podFindings(pod PodDiagnostic, since time.Time) []Finding {
	var findings []Finding
	for _, container := range pod.Containers {
		findings = append(findings, containerFindings(pod.Name, container, since)...)
	}

	anyWaiting := false
	for _, container := range pod.Containers {
		if container.WaitingReason != "" {
			anyWaiting = true
			break
		}
	}

	switch {
	case pod.Phase == "Pending" && !pod.Scheduling.Schedulable:
		detail := pod.Scheduling.Message
		if detail == "" {
			detail = pod.Scheduling.Reason
		}
		if detail == "" {
			detail = "unschedulable"
		}
		// Reason first, pod name after — the shape every other container-level
		// finding already uses ("CrashLoopBackOff in pod x", "OOMKilled in pod
		// x"). It matters most here: this is the longest finding kx produces,
		// the scheduler's message runs to a couple of hundred characters, and
		// name-first meant the triage table's ellipsis landed mid-word before
		// the row had said anything ("Pod report-unschedulable-57d7… unsc…").
		findings = append(findings, finding(Critical, Cause, fmt.Sprintf(
			"Unschedulable: %s (pod %s)", detail, pod.Name)))
	case pod.Phase == "Pending":
		findings = append(findings, finding(Warning, Aggregate, "Pod "+pod.Name+" pending"))
	case pod.Phase == "Failed":
		findings = append(findings, finding(Critical, Aggregate, "Pod "+pod.Name+" failed"))
	case pod.Phase == "Running" && pod.ReadyContainers < pod.TotalContainers && !anyWaiting:
		// A waiting container already produced its own, more specific finding.
		findings = append(findings, finding(Warning, Aggregate, fmt.Sprintf(
			"Pod %s: %d/%d containers ready",
			pod.Name, pod.ReadyContainers, pod.TotalContainers)))
	}
	return findings
}

func containerFindings(podName string, container ContainerDiagnostic, since time.Time) []Finding {
	var findings []Finding
	reason := container.WaitingReason
	// What the container is doing now is reported however old it is; what it
	// did last time is reported only if it happened inside the window. A
	// container that OOMKilled once and has been running ever since has been
	// superseded by that evidence, and reporting it forever held a healthy
	// workload at critical.
	settled := outsideWindow(container.LastTerminatedAt, since)

	switch {
	case reason == "CrashLoopBackOff":
		findings = append(findings, finding(Critical, Cause, fmt.Sprintf(
			"CrashLoopBackOff in pod %s (%d restarts)", podName, container.RestartCount)))
	case imagePullReasons[reason]:
		findings = append(findings, finding(Critical, Cause, fmt.Sprintf(
			"Image pull failure (%s) in pod %s", reason, podName)))
	case configErrorReasons[reason]:
		detail := container.WaitingMessage
		if detail == "" {
			detail = reason
		}
		findings = append(findings, finding(Critical, Cause, fmt.Sprintf(
			"Container config error in pod %s: %s", podName, detail)))
	case reason != "":
		findings = append(findings, finding(Warning, Cause, fmt.Sprintf(
			"Container %s in pod %s waiting: %s", container.Name, podName, reason)))
	}

	switch {
	// Two OOMKills, told apart by which state they are in: the current one
	// is happening now and carries no moment, the previous one happened at
	// one and is dated by it.
	case container.TerminatedReason == "OOMKilled":
		findings = append(findings, finding(Critical, Cause, "OOMKilled in pod "+podName))
	case container.LastTerminatedReason == "OOMKilled" && !settled:
		findings = append(findings, dated(Critical, Cause, container.LastTerminatedAt,
			"OOMKilled in pod "+podName))
	case container.TerminatedReason != "" && container.TerminatedReason != "Completed" &&
		container.ExitCode != nil && *container.ExitCode != 0:
		findings = append(findings, finding(Critical, Cause, fmt.Sprintf(
			"Container %s in pod %s terminated: %s (exit %d)",
			container.Name, podName, container.TerminatedReason, *container.ExitCode)))
	}

	// Only when not waiting: a CrashLoopBackOff finding already reports the
	// restart count, and repeating it adds nothing.
	// The count is cumulative over the pod's whole life, so the last
	// termination is what says whether the thrashing is current.
	if reason == "" && !settled && container.RestartCount >= restartWarnThreshold {
		findings = append(findings, dated(Warning, Cause, container.LastTerminatedAt, fmt.Sprintf(
			"Container %s in pod %s restarted %d times",
			container.Name, podName, container.RestartCount)))
	}
	return append(findings, usageFindings(container)...)
}

// usageFindings is silent when there is no limit to compare against, and when
// there is no usage data (metrics-server unavailable, or the container simply
// isn't in the metrics response). Neither is a defect.
func usageFindings(container ContainerDiagnostic) []Finding {
	var findings []Finding

	if pct, ok := usagePercent(container.MemoryUsage, container.MemoryLimit); ok {
		switch {
		case pct >= memoryCriticalPct:
			findings = append(findings, finding(Critical, Cause, fmt.Sprintf(
				"Memory at %d%% of limit (%s/%s) — OOMKill risk",
				pct, formatMemory(container.MemoryUsage), formatMemory(container.MemoryLimit))))
		case pct >= memoryWarnPct:
			findings = append(findings, finding(Warning, Cause, fmt.Sprintf(
				"Memory at %d%% of limit (%s/%s)",
				pct, formatMemory(container.MemoryUsage), formatMemory(container.MemoryLimit))))
		}
	}

	if pct, ok := usagePercent(container.CPUUsage, container.CPULimit); ok && pct >= cpuWarnPct {
		findings = append(findings, finding(Warning, Cause, fmt.Sprintf(
			"CPU at %d%% of limit (%s/%s) — likely throttling",
			pct, formatCPU(container.CPUUsage), formatCPU(container.CPULimit))))
	}
	return findings
}

// usagePercent computes usage against a limit as a truncated percentage.
// Quantities are compared scaled to milli so sub-core CPU doesn't truncate to
// zero.
func usagePercent(usage, limit *resource.Quantity) (int64, bool) {
	if usage == nil || limit == nil || limit.IsZero() {
		return 0, false
	}
	total := limit.ScaledValue(resource.Milli)
	if total == 0 {
		return 0, false
	}
	return usage.ScaledValue(resource.Milli) * 100 / total, true
}

func formatMemory(value *resource.Quantity) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%dMi", value.Value()/(1024*1024))
}

func formatCPU(value *resource.Quantity) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%dm", value.ScaledValue(resource.Milli))
}

// eventFindings omits the message deliberately: the WARNING EVENTS section
// renders it in full, so repeating it here only bloats the summary.
func eventFindings(events []EventSummary) []Finding {
	findings := make([]Finding, 0, len(events))
	for _, event := range events {
		findings = append(findings, dated(Warning, Event, event.LastTimestamp, fmt.Sprintf(
			"%s ×%d on %s/%s", event.Reason, event.Count, event.Kind, event.Name)))
	}
	return findings
}
