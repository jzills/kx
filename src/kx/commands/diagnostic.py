from dataclasses import dataclass, field

from kx.diagnostics import (
    ContainerDiagnostic,
    CronJobHealth,
    DiagnosticData,
    DiagnosticReport,
    DiagnosticsServiceProtocol,
    Finding,
    JobHealth,
    PodDiagnostic,
    PVCHealth,
    ReplicaHealth,
    ServiceHealth,
    Severity,
)
from kx.kinds import Kind
from kx.state import State, StateServiceProtocol

_SUPPORTED_KINDS = {
    Kind.Deployment,
    Kind.StatefulSet,
    Kind.DaemonSet,
    Kind.Pod,
    Kind.Job,
    Kind.Service,
    Kind.PersistentVolumeClaim,
    Kind.CronJob,
}
_RESTART_WARN_THRESHOLD = 5

_IMAGE_PULL_REASONS = {"ImagePullBackOff", "ErrImagePull", "InvalidImageName"}
_CONFIG_ERROR_REASONS = {"CreateContainerConfigError", "CreateContainerError"}


class DiagnosticCommand:
    def __init__(
        self,
        state: StateServiceProtocol,
        diagnostics: DiagnosticsServiceProtocol,
    ):
        self.state = state
        self.diagnostics = diagnostics

    def execute(self, index: int) -> DiagnosticReport:
        name, namespace, kind = self.state.fields(index)
        if kind not in _SUPPORTED_KINDS:
            raise ValueError(f"diagnostic is not supported for '{kind}'.")
        return build_report(self.diagnostics.gather(kind, name, namespace))


@dataclass
class TriageResult:
    namespace: str
    checked: int  # total resources swept
    reports: list[DiagnosticReport]  # non-OK only, sorted critical → warning
    healthy: int  # OK resources, collapsed out of the table
    dropped: list[str] = field(default_factory=list)  # rows lost to name collisions


class TriageCommand:
    def __init__(
        self,
        state: StateServiceProtocol,
        diagnostics: DiagnosticsServiceProtocol,
    ):
        self.state = state
        self.diagnostics = diagnostics

    def execute(self, namespace: str) -> TriageResult:
        reports = [build_report(data) for data in self.diagnostics.sweep(namespace)]
        unhealthy = sorted(
            (r for r in reports if r.verdict != Severity.OK),
            key=lambda r: r.verdict,
            reverse=True,  # stable: sweep order preserved within a severity
        )
        # State.resources is keyed by name alone, so a rare cross-kind name
        # collision keeps the earlier (more severe) row and drops the later one.
        resources: dict[str, Kind | str] = {}
        displayed: list[DiagnosticReport] = []
        dropped: list[str] = []
        for report in unhealthy:
            if report.name in resources:
                dropped.append(f"{report.kind}/{report.name}")
                continue
            resources[report.name] = report.kind
            displayed.append(report)
        if displayed:
            self.state.save(State(resources=resources, namespace=namespace))
        return TriageResult(
            namespace=namespace,
            checked=len(reports),
            reports=displayed,
            healthy=len(reports) - len(unhealthy),
            dropped=dropped,
        )


def build_report(data: DiagnosticData) -> DiagnosticReport:
    findings: list[Finding] = []

    if data.replicas is not None:
        findings.extend(_replica_findings(data.replicas))
    if data.job is not None:
        findings.extend(_job_findings(data.job))
    if data.service is not None:
        findings.extend(_service_findings(data.service))
    if data.pvc is not None:
        findings.extend(_pvc_findings(data.pvc))
    if data.cronjob is not None:
        findings.extend(_cronjob_findings(data.cronjob))
    for pod in data.pods:
        findings.extend(_pod_findings(pod))
    findings.extend(_event_findings(data.warning_events))

    # Prioritize: highest severity first, stable within a severity.
    findings.sort(key=lambda f: f.severity, reverse=True)
    verdict = max((f.severity for f in findings), default=Severity.OK)

    return DiagnosticReport(
        kind=data.kind,
        name=data.name,
        namespace=data.namespace,
        verdict=verdict,
        findings=findings,
        pods=data.pods,
        warning_events=data.warning_events,
    )


def _replica_findings(replicas: ReplicaHealth) -> list[Finding]:
    findings: list[Finding] = []
    gen = replicas.generation
    observed = replicas.observed_generation
    if gen is not None and observed is not None and observed < gen:
        findings.append(
            Finding(
                Severity.CRITICAL,
                f"Rollout stalled: observed generation {observed} behind spec generation {gen}",
            )
        )
    if replicas.ready < replicas.desired:
        severity = (
            Severity.CRITICAL
            if replicas.ready == 0 and replicas.desired > 0
            else Severity.WARNING
        )
        findings.append(
            Finding(
                severity,
                f"Only {replicas.ready}/{replicas.desired} replicas ready",
            )
        )
    if replicas.available < replicas.desired:
        findings.append(
            Finding(
                Severity.WARNING,
                f"{replicas.available}/{replicas.desired} replicas available",
            )
        )
    if replicas.updated < replicas.desired:
        findings.append(
            Finding(
                Severity.WARNING,
                f"Rollout in progress: {replicas.updated}/{replicas.desired} replicas updated",
            )
        )
    return findings


def _job_findings(job: JobHealth) -> list[Finding]:
    """Suspended, active, and successfully-completed Jobs are all OK — the
    same treatment a Deployment scaled to 0 replicas already gets from
    _replica_findings. Any trouble in the Job's own pods (CrashLoopBackOff,
    ImagePullBackOff, ...) surfaces separately via _pod_findings."""
    findings: list[Finding] = []
    if job.backoff_limit_exceeded:
        findings.append(
            Finding(
                Severity.CRITICAL,
                f"BackoffLimitExceeded ({job.failed}/{job.backoff_limit} failed)",
            )
        )
    if job.deadline_exceeded:
        findings.append(Finding(Severity.CRITICAL, "DeadlineExceeded"))
    return findings


def _service_findings(svc: ServiceHealth) -> list[Finding]:
    """No selector (ExternalName, headless, manually-managed Endpoints) is a
    legitimate configuration, not a defect — no finding either way. Any real
    trouble in the Service's backing pods surfaces separately via
    _pod_findings, same as for any other kind."""
    if not svc.has_selector:
        return []
    total = svc.ready_addresses + svc.not_ready_addresses
    if total == 0:
        return [Finding(Severity.CRITICAL, "No endpoints: no pods match the selector")]
    if svc.ready_addresses == 0:
        return [
            Finding(
                Severity.CRITICAL,
                f"{svc.not_ready_addresses} endpoint(s) not ready, 0 ready",
            )
        ]
    if svc.not_ready_addresses > 0:
        return [
            Finding(Severity.WARNING, f"{svc.ready_addresses}/{total} endpoints ready")
        ]
    return []


def _cronjob_findings(cj: CronJobHealth) -> list[Finding]:
    """No 'missed schedule' detection (would need cron-expression parsing,
    not a dependency here). Instead a rollup of the most recently-run owned
    Job, reusing _job_findings directly so a CronJob whose last run hit
    BackoffLimitExceeded/DeadlineExceeded shows up the same way a standalone
    failed Job does. Suspended and never-run are both OK — not enough signal
    to call a fresh or paused CronJob broken."""
    if cj.suspended or cj.most_recent_job is None:
        return []
    return [
        Finding(finding.severity, f"Most recent run: {finding.summary}")
        for finding in _job_findings(cj.most_recent_job)
    ]


def _pvc_findings(pvc: PVCHealth) -> list[Finding]:
    """No duration threshold — Pending is flagged immediately, mirroring the
    unconditional "Pod pending" finding rather than a time-gated heuristic."""
    if pvc.phase == "Pending":
        return [Finding(Severity.WARNING, "PersistentVolumeClaim pending")]
    if pvc.phase == "Lost":
        return [
            Finding(
                Severity.CRITICAL,
                "PersistentVolumeClaim lost: backing volume no longer available",
            )
        ]
    return []


def _pod_findings(pod: PodDiagnostic) -> list[Finding]:
    findings: list[Finding] = []

    for container in pod.containers:
        findings.extend(_container_findings(pod.name, container))

    if pod.phase == "Pending" and not pod.scheduling.schedulable:
        detail = pod.scheduling.message or pod.scheduling.reason or "unschedulable"
        findings.append(
            Finding(Severity.CRITICAL, f"Pod {pod.name} unschedulable: {detail}")
        )
    elif pod.phase == "Pending":
        findings.append(Finding(Severity.WARNING, f"Pod {pod.name} pending"))
    elif pod.phase == "Failed":
        findings.append(Finding(Severity.CRITICAL, f"Pod {pod.name} failed"))
    elif (
        pod.phase == "Running"
        and pod.ready_containers < pod.total_containers
        and not any(c.waiting_reason for c in pod.containers)
    ):
        findings.append(
            Finding(
                Severity.WARNING,
                f"Pod {pod.name}: {pod.ready_containers}/{pod.total_containers} containers ready",
            )
        )
    return findings


def _container_findings(pod_name: str, container: ContainerDiagnostic) -> list[Finding]:
    findings: list[Finding] = []
    reason = container.waiting_reason

    if reason == "CrashLoopBackOff":
        findings.append(
            Finding(
                Severity.CRITICAL,
                f"CrashLoopBackOff in pod {pod_name} ({container.restart_count} restarts)",
            )
        )
    elif reason in _IMAGE_PULL_REASONS:
        findings.append(
            Finding(
                Severity.CRITICAL,
                f"Image pull failure ({reason}) in pod {pod_name}",
            )
        )
    elif reason in _CONFIG_ERROR_REASONS:
        detail = container.waiting_message or reason
        findings.append(
            Finding(
                Severity.CRITICAL,
                f"Container config error in pod {pod_name}: {detail}",
            )
        )
    elif reason is not None:
        findings.append(
            Finding(
                Severity.WARNING,
                f"Container {container.name} in pod {pod_name} waiting: {reason}",
            )
        )

    if "OOMKilled" in (container.terminated_reason, container.last_terminated_reason):
        findings.append(Finding(Severity.CRITICAL, f"OOMKilled in pod {pod_name}"))
    elif container.terminated_reason not in (None, "Completed") and container.exit_code:
        findings.append(
            Finding(
                Severity.CRITICAL,
                f"Container {container.name} in pod {pod_name} terminated: "
                f"{container.terminated_reason} (exit {container.exit_code})",
            )
        )

    if reason is None and container.restart_count >= _RESTART_WARN_THRESHOLD:
        findings.append(
            Finding(
                Severity.WARNING,
                f"Container {container.name} in pod {pod_name} "
                f"restarted {container.restart_count} times",
            )
        )
    return findings


def _event_findings(events) -> list[Finding]:
    # The message is deliberately omitted: the WARNING EVENTS section renders it
    # in full, so repeating it here only bloats the summary.
    return [
        Finding(
            Severity.WARNING,
            f"{event.reason} ×{event.count} on {event.kind}/{event.name}",
        )
        for event in events
    ]
