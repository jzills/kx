package diagnostics

import (
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/jzills/kx/internal/kinds"
)

// SupportedKinds are the kinds kx diag can analyse.
var SupportedKinds = map[kinds.Kind]bool{
	kinds.Deployment:            true,
	kinds.StatefulSet:           true,
	kinds.DaemonSet:             true,
	kinds.Pod:                   true,
	kinds.Job:                   true,
	kinds.Service:               true,
	kinds.PersistentVolumeClaim: true,
	kinds.CronJob:               true,
	kinds.Ingress:               true,
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

	if data.Replicas != nil {
		findings = append(findings, replicaFindings(*data.Replicas)...)
	}
	if data.Job != nil {
		findings = append(findings, jobFindings(*data.Job)...)
	}
	if data.Service != nil {
		findings = append(findings, serviceFindings(*data.Service)...)
	}
	if data.PVC != nil {
		findings = append(findings, pvcFindings(*data.PVC)...)
	}
	if data.CronJob != nil {
		findings = append(findings, cronJobFindings(*data.CronJob)...)
	}
	if data.Ingress != nil {
		findings = append(findings, ingressFindings(*data.Ingress)...)
	}
	for _, pod := range data.Pods {
		findings = append(findings, podFindings(pod)...)
	}
	findings = append(findings, eventFindings(data.WarningEvents)...)

	// Highest severity first, stable within a severity so the order findings
	// were produced in survives.
	sort.SliceStable(findings, func(i, j int) bool {
		return findings[i].Severity > findings[j].Severity
	})

	verdict := OK
	for _, finding := range findings {
		if finding.Severity > verdict {
			verdict = finding.Severity
		}
	}

	return Report{
		Kind:          data.Kind,
		Name:          data.Name,
		Namespace:     data.Namespace,
		Verdict:       verdict,
		Findings:      findings,
		Pods:          data.Pods,
		WarningEvents: data.WarningEvents,
	}
}

func replicaFindings(replicas ReplicaHealth) []Finding {
	var findings []Finding
	if replicas.Generation != nil && replicas.ObservedGeneration != nil &&
		*replicas.ObservedGeneration < *replicas.Generation {
		findings = append(findings, Finding{Critical, fmt.Sprintf(
			"Rollout stalled: observed generation %d behind spec generation %d",
			*replicas.ObservedGeneration, *replicas.Generation)})
	}
	if replicas.Ready < replicas.Desired {
		severity := Warning
		if replicas.Ready == 0 && replicas.Desired > 0 {
			severity = Critical
		}
		findings = append(findings, Finding{severity, fmt.Sprintf(
			"Only %d/%d replicas ready", replicas.Ready, replicas.Desired)})
	}
	if replicas.Available < replicas.Desired {
		findings = append(findings, Finding{Warning, fmt.Sprintf(
			"%d/%d replicas available", replicas.Available, replicas.Desired)})
	}
	if replicas.Updated < replicas.Desired {
		findings = append(findings, Finding{Warning, fmt.Sprintf(
			"Rollout in progress: %d/%d replicas updated", replicas.Updated, replicas.Desired)})
	}
	return findings
}

// jobFindings treats suspended, active and successfully completed Jobs as OK —
// the same treatment a Deployment scaled to zero already gets. Trouble in the
// Job's own pods surfaces separately through podFindings.
func jobFindings(job JobHealth) []Finding {
	var findings []Finding
	if job.BackoffLimitExceeded {
		findings = append(findings, Finding{Critical, fmt.Sprintf(
			"BackoffLimitExceeded (%d/%d failed)", job.Failed, job.BackoffLimit)})
	}
	if job.DeadlineExceeded {
		findings = append(findings, Finding{Critical, "DeadlineExceeded"})
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
		return []Finding{{Critical, "No endpoints: no pods match the selector"}}
	case service.ReadyAddresses == 0:
		return []Finding{{Critical, fmt.Sprintf(
			"%d endpoint(s) not ready, 0 ready", service.NotReadyAddresses)}}
	case service.NotReadyAddresses > 0:
		return []Finding{{Warning, fmt.Sprintf(
			"%d/%d endpoints ready", service.ReadyAddresses, total)}}
	}
	return nil
}

// cronJobFindings rolls up the most recent run, reusing jobFindings so a
// CronJob whose last run hit BackoffLimitExceeded reads the same way a
// standalone failed Job does. Suspended and never-run are both OK — not enough
// signal to call a fresh or paused CronJob broken.
func cronJobFindings(cronJob CronJobHealth) []Finding {
	if cronJob.Suspended || cronJob.MostRecentJob == nil {
		return nil
	}
	var findings []Finding
	for _, finding := range jobFindings(*cronJob.MostRecentJob) {
		findings = append(findings, Finding{
			finding.Severity, "Most recent run: " + finding.Summary,
		})
	}
	return findings
}

// pvcFindings flags Pending immediately rather than after a duration, mirroring
// the unconditional "Pod pending" finding.
func pvcFindings(pvc PVCHealth) []Finding {
	switch pvc.Phase {
	case "Pending":
		return []Finding{{Warning, "PersistentVolumeClaim pending"}}
	case "Lost":
		return []Finding{{Critical,
			"PersistentVolumeClaim lost: backing volume no longer available"}}
	}
	return nil
}

// ingressFindings reports one Critical finding per backend Service the
// Ingress references but which does not exist. No loadBalancer/address
// check — see IngressHealth's doc comment.
func ingressFindings(ingress IngressHealth) []Finding {
	findings := make([]Finding, 0, len(ingress.MissingBackends))
	for _, name := range ingress.MissingBackends {
		findings = append(findings, Finding{Critical, fmt.Sprintf(
			"Ingress references missing Service '%s'", name)})
	}
	return findings
}

func podFindings(pod PodDiagnostic) []Finding {
	var findings []Finding
	for _, container := range pod.Containers {
		findings = append(findings, containerFindings(pod.Name, container)...)
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
		findings = append(findings, Finding{Critical, fmt.Sprintf(
			"Pod %s unschedulable: %s", pod.Name, detail)})
	case pod.Phase == "Pending":
		findings = append(findings, Finding{Warning, "Pod " + pod.Name + " pending"})
	case pod.Phase == "Failed":
		findings = append(findings, Finding{Critical, "Pod " + pod.Name + " failed"})
	case pod.Phase == "Running" && pod.ReadyContainers < pod.TotalContainers && !anyWaiting:
		// A waiting container already produced its own, more specific finding.
		findings = append(findings, Finding{Warning, fmt.Sprintf(
			"Pod %s: %d/%d containers ready",
			pod.Name, pod.ReadyContainers, pod.TotalContainers)})
	}
	return findings
}

func containerFindings(podName string, container ContainerDiagnostic) []Finding {
	var findings []Finding
	reason := container.WaitingReason

	switch {
	case reason == "CrashLoopBackOff":
		findings = append(findings, Finding{Critical, fmt.Sprintf(
			"CrashLoopBackOff in pod %s (%d restarts)", podName, container.RestartCount)})
	case imagePullReasons[reason]:
		findings = append(findings, Finding{Critical, fmt.Sprintf(
			"Image pull failure (%s) in pod %s", reason, podName)})
	case configErrorReasons[reason]:
		detail := container.WaitingMessage
		if detail == "" {
			detail = reason
		}
		findings = append(findings, Finding{Critical, fmt.Sprintf(
			"Container config error in pod %s: %s", podName, detail)})
	case reason != "":
		findings = append(findings, Finding{Warning, fmt.Sprintf(
			"Container %s in pod %s waiting: %s", container.Name, podName, reason)})
	}

	switch {
	case container.TerminatedReason == "OOMKilled" || container.LastTerminatedReason == "OOMKilled":
		findings = append(findings, Finding{Critical, "OOMKilled in pod " + podName})
	case container.TerminatedReason != "" && container.TerminatedReason != "Completed" &&
		container.ExitCode != nil && *container.ExitCode != 0:
		findings = append(findings, Finding{Critical, fmt.Sprintf(
			"Container %s in pod %s terminated: %s (exit %d)",
			container.Name, podName, container.TerminatedReason, *container.ExitCode)})
	}

	// Only when not waiting: a CrashLoopBackOff finding already reports the
	// restart count, and repeating it adds nothing.
	if reason == "" && container.RestartCount >= restartWarnThreshold {
		findings = append(findings, Finding{Warning, fmt.Sprintf(
			"Container %s in pod %s restarted %d times",
			container.Name, podName, container.RestartCount)})
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
			findings = append(findings, Finding{Critical, fmt.Sprintf(
				"Memory at %d%% of limit (%s/%s) — OOMKill risk",
				pct, formatMemory(container.MemoryUsage), formatMemory(container.MemoryLimit))})
		case pct >= memoryWarnPct:
			findings = append(findings, Finding{Warning, fmt.Sprintf(
				"Memory at %d%% of limit (%s/%s)",
				pct, formatMemory(container.MemoryUsage), formatMemory(container.MemoryLimit))})
		}
	}

	if pct, ok := usagePercent(container.CPUUsage, container.CPULimit); ok && pct >= cpuWarnPct {
		findings = append(findings, Finding{Warning, fmt.Sprintf(
			"CPU at %d%% of limit (%s/%s) — likely throttling",
			pct, formatCPU(container.CPUUsage), formatCPU(container.CPULimit))})
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
		findings = append(findings, Finding{Warning, fmt.Sprintf(
			"%s ×%d on %s/%s", event.Reason, event.Count, event.Kind, event.Name)})
	}
	return findings
}
