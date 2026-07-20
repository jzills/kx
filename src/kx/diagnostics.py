import re
from dataclasses import dataclass, field
from datetime import datetime
from enum import IntEnum
from typing import Protocol

from kubernetes import client

from kx.events import EventsServiceProtocol
from kx.graph import _owned_by, resolve_workload_pods
from kx.k8s import load_config
from kx.kinds import Kind


class Severity(IntEnum):
    """Ordered so max() over findings yields the overall verdict."""

    OK = 0
    WARNING = 1
    CRITICAL = 2


# OTEL-aligned severity tokens used to surface error lines from container logs.
SEVERITY_PATTERN = re.compile(
    r"\b(FATAL|CRITICAL|ERROR|ERR|WARNING|WARN|EXCEPTION|TRACEBACK|PANIC)\b",
    re.IGNORECASE,
)
_LOG_TAIL_LINES = 50  # lines requested from the API per container
_LOG_MAX_MATCHES = 8  # severity-matching lines shown
_LOG_FALLBACK_LINES = 3  # raw tail shown when nothing matches


def filter_severity_lines(
    raw_lines: list[str],
    max_matches: int = _LOG_MAX_MATCHES,
    fallback: int = _LOG_FALLBACK_LINES,
) -> tuple[list[str], bool]:
    """Select the most relevant log lines: the last `max_matches` lines matching
    an OTEL severity token, or — if none match — the last `fallback` raw lines so
    a failing container always shows something. Returns (lines, matched_severity)."""
    lines = [line.rstrip() for line in raw_lines if line.strip()]
    matches = [line for line in lines if SEVERITY_PATTERN.search(line)]
    if matches:
        return matches[-max_matches:], True
    return lines[-fallback:], False


# --- data model -------------------------------------------------------------


@dataclass
class ContainerDiagnostic:
    name: str
    ready: bool
    started: bool | None
    restart_count: int
    state: str  # "Running" | "Waiting" | "Terminated" | "Unknown"
    waiting_reason: str | None = None
    waiting_message: str | None = None
    terminated_reason: str | None = None
    exit_code: int | None = None
    last_terminated_reason: str | None = None
    last_exit_code: int | None = None
    log_lines: list[str] = field(default_factory=list)
    log_source: str | None = None  # "previous" | "current"
    log_filtered: bool = True  # False when log_lines is a raw fallback tail


@dataclass
class SchedulingInfo:
    schedulable: bool
    reason: str | None = None
    message: str | None = None


@dataclass
class PodDiagnostic:
    name: str
    phase: str
    node: str | None
    ready_containers: int
    total_containers: int
    containers: list[ContainerDiagnostic]
    scheduling: SchedulingInfo


@dataclass
class ReplicaHealth:
    desired: int
    ready: int
    available: int
    updated: int
    generation: int | None = None
    observed_generation: int | None = None


@dataclass
class ServiceHealth:
    """The Service's own spec/status carries no health signal — this is built
    from its Endpoints object, the same source kubectl and the cluster use to
    decide where traffic actually routes."""

    has_selector: bool
    ready_addresses: int
    not_ready_addresses: int


@dataclass
class PVCHealth:
    """No pod fan-out, no ownership — a PVC's status is self-contained."""

    phase: str  # "Pending" | "Bound" | "Lost" | "Unknown"


@dataclass
class JobHealth:
    """Doesn't reuse ReplicaHealth: a Job has no desired/ready replica concept
    — completion/failure counts and a backoff limit instead."""

    succeeded: int
    failed: int
    active: int
    suspended: bool
    backoff_limit: int
    backoff_limit_exceeded: bool
    deadline_exceeded: bool


@dataclass
class EventSummary:
    reason: str
    message: str
    kind: str
    name: str
    count: int
    last_timestamp: datetime | None = None


@dataclass
class DiagnosticData:
    """Raw, already-flattened data produced by the service (no findings)."""

    kind: str
    name: str
    namespace: str
    replicas: ReplicaHealth | None
    job: JobHealth | None = None
    service: ServiceHealth | None = None
    pvc: PVCHealth | None = None
    pods: list[PodDiagnostic] = field(default_factory=list)
    warning_events: list[EventSummary] = field(default_factory=list)


@dataclass
class Finding:
    severity: Severity
    summary: str


@dataclass
class DiagnosticReport:
    """Analysed report produced by the command from DiagnosticData.

    Replica health is not carried here: _replica_findings distills it into
    findings (ready/available/updated shortfalls, generation lag), so the
    rendered report has no separate replica section."""

    kind: str
    name: str
    namespace: str
    verdict: Severity  # == max finding severity (OK if none)
    findings: list[Finding]  # sorted severity desc, stable
    pods: list[PodDiagnostic] = field(default_factory=list)
    warning_events: list[EventSummary] = field(default_factory=list)


# --- service ----------------------------------------------------------------


class DiagnosticsServiceProtocol(Protocol):
    def gather(self, kind: str, name: str, namespace: str) -> DiagnosticData: ...
    def sweep(self, namespace: str) -> list[DiagnosticData]: ...


class DiagnosticsService:
    def __init__(self, events: EventsServiceProtocol):
        self.events = events

    def gather(self, kind: str, name: str, namespace: str) -> DiagnosticData:
        load_config()
        apps = client.AppsV1Api()
        core = client.CoreV1Api()
        batch = client.BatchV1Api()

        replicas = self._replica_health(kind, name, namespace, apps)
        job = self._job_health(kind, name, namespace, batch)
        service = self._service_health(kind, name, namespace, core)
        pvc = self._pvc_health(kind, name, namespace, core)
        pods_raw = resolve_workload_pods(kind, name, namespace, apps, core, batch)
        pods = [_pod_diagnostic(pod) for pod in pods_raw]
        self._attach_logs(pods, namespace, core)
        warning_events = self._warning_events(kind, name, namespace, pods_raw)

        return DiagnosticData(
            kind=kind,
            name=name,
            namespace=namespace,
            replicas=replicas,
            job=job,
            service=service,
            pvc=pvc,
            pods=pods,
            warning_events=warning_events,
        )

    def sweep(self, namespace: str) -> list[DiagnosticData]:
        """Diagnose every workload in the namespace plus orphan pods (pods not
        owned by a swept workload — bare pods, Job pods). One list call per
        kind, one events fetch, and no log tails: logs feed the detailed LOGS
        section only, never findings, so the sweep skips them for speed."""
        load_config()
        apps = client.AppsV1Api()
        core = client.CoreV1Api()
        batch = client.BatchV1Api()

        pods = core.list_namespaced_pod(namespace).items
        replica_sets = apps.list_namespaced_replica_set(namespace).items
        all_events = self.events.get(namespace)

        workloads: list[tuple[Kind, object, set[str]]] = []
        for deploy in apps.list_namespaced_deployment(namespace).items:
            rs_uids = {
                rs.metadata.uid
                for rs in replica_sets
                if _owned_by(rs, deploy.metadata.uid)
            }
            workloads.append((Kind.Deployment, deploy, rs_uids))
        for sts in apps.list_namespaced_stateful_set(namespace).items:
            workloads.append((Kind.StatefulSet, sts, {sts.metadata.uid}))
        for ds in apps.list_namespaced_daemon_set(namespace).items:
            workloads.append((Kind.DaemonSet, ds, {ds.metadata.uid}))
        for job in batch.list_namespaced_job(namespace).items:
            workloads.append((Kind.Job, job, {job.metadata.uid}))

        results: list[DiagnosticData] = []
        claimed: set[str] = set()
        for kind, obj, owner_uids in workloads:
            owned = [
                pod for pod in pods if any(_owned_by(pod, uid) for uid in owner_uids)
            ]
            claimed.update(pod.metadata.uid for pod in owned)
            results.append(
                self._build_data(
                    kind, obj.metadata.name, namespace, obj, owned, all_events
                )
            )

        # Services are matched by label selector, not ownership: their pods
        # are independently owned (or genuinely unowned) elsewhere and are
        # never excluded from the orphan pass below on a Service's account.
        endpoints_by_name = {
            ep.metadata.name: ep
            for ep in core.list_namespaced_endpoints(namespace).items
        }
        for svc in core.list_namespaced_service(namespace).items:
            selector = svc.spec.selector or {}
            svc_pods = [
                pod for pod in pods if selector and _matches_selector(pod, selector)
            ]
            results.append(
                DiagnosticData(
                    kind=Kind.Service,
                    name=svc.metadata.name,
                    namespace=namespace,
                    replicas=None,
                    service=_service_health_from(
                        svc, endpoints_by_name.get(svc.metadata.name)
                    ),
                    pods=[_pod_diagnostic(pod) for pod in svc_pods],
                    warning_events=self._warning_events(
                        Kind.Service,
                        svc.metadata.name,
                        namespace,
                        svc_pods,
                        all_events=all_events,
                    ),
                )
            )

        # PVCs have no pods and no ownership relationships — simplest of the
        # sweep's additions, just a listing and a phase check.
        for claim in core.list_namespaced_persistent_volume_claim(namespace).items:
            results.append(
                DiagnosticData(
                    kind=Kind.PersistentVolumeClaim,
                    name=claim.metadata.name,
                    namespace=namespace,
                    replicas=None,
                    pvc=PVCHealth(
                        phase=getattr(claim.status, "phase", None) or "Unknown"
                    ),
                    warning_events=self._warning_events(
                        Kind.PersistentVolumeClaim,
                        claim.metadata.name,
                        namespace,
                        [],
                        all_events=all_events,
                    ),
                )
            )

        for pod in pods:
            if pod.metadata.uid in claimed:
                continue
            results.append(
                self._build_data(
                    Kind.Pod, pod.metadata.name, namespace, None, [pod], all_events
                )
            )
        return results

    def _build_data(
        self, kind, name, namespace, obj, pods_raw, all_events
    ) -> DiagnosticData:
        return DiagnosticData(
            kind=kind,
            name=name,
            namespace=namespace,
            # Both extractors already return None for kinds they don't handle
            # (Job has no ReplicaHealth; anything else has no JobHealth).
            replicas=_replica_health_from(kind, obj) if obj is not None else None,
            job=_job_health_from(obj) if obj is not None and kind == Kind.Job else None,
            pods=[_pod_diagnostic(pod) for pod in pods_raw],
            warning_events=self._warning_events(
                kind, name, namespace, pods_raw, all_events=all_events
            ),
        )

    def _replica_health(self, kind, name, namespace, apps) -> ReplicaHealth | None:
        if kind == Kind.Deployment:
            return _replica_health_from(
                kind, apps.read_namespaced_deployment(name, namespace)
            )
        if kind == Kind.StatefulSet:
            return _replica_health_from(
                kind, apps.read_namespaced_stateful_set(name, namespace)
            )
        if kind == Kind.DaemonSet:
            return _replica_health_from(
                kind, apps.read_namespaced_daemon_set(name, namespace)
            )
        return None

    def _job_health(self, kind, name, namespace, batch) -> JobHealth | None:
        if kind != Kind.Job:
            return None
        return _job_health_from(batch.read_namespaced_job(name, namespace))

    def _service_health(self, kind, name, namespace, core) -> ServiceHealth | None:
        if kind != Kind.Service:
            return None
        svc = core.read_namespaced_service(name, namespace)
        try:
            endpoints = core.read_namespaced_endpoints(name, namespace)
        except Exception:
            endpoints = None
        return _service_health_from(svc, endpoints)

    def _pvc_health(self, kind, name, namespace, core) -> PVCHealth | None:
        if kind != Kind.PersistentVolumeClaim:
            return None
        claim = core.read_namespaced_persistent_volume_claim(name, namespace)
        return PVCHealth(phase=getattr(claim.status, "phase", None) or "Unknown")

    def _attach_logs(self, pods, namespace, core) -> None:
        """Fetch and filter a log excerpt for every unhealthy container. Healthy,
        ready, never-restarted containers are skipped so healthy reports stay
        clean and fast."""
        for pod in pods:
            for container in pod.containers:
                if not _container_needs_logs(container):
                    continue
                raw, source = self._fetch_log_tail(
                    namespace, pod.name, container.name, container.state, core
                )
                if not raw:
                    continue
                container.log_lines, container.log_filtered = filter_severity_lines(raw)
                container.log_source = source

    def _fetch_log_tail(self, namespace, pod_name, container_name, state, core):
        """Return (lines, source). A running container yields its current tail; a
        crashed/waiting one prefers the previous (dead) instance, falling back to
        current. Any API error (e.g. no previous logs yet) yields ([], None)."""
        attempts = [False] if state == "Running" else [True, False]
        for previous in attempts:
            try:
                # _preload_content=False returns the raw response; reading .data
                # avoids the client's read_namespaced_pod_log quirk of returning
                # a bytes-repr string ("b'...\\n...'") that never splits on lines.
                resp = core.read_namespaced_pod_log(
                    name=pod_name,
                    namespace=namespace,
                    container=container_name,
                    previous=previous,
                    tail_lines=_LOG_TAIL_LINES,
                    _preload_content=False,
                )
                text = resp.data.decode("utf-8", errors="replace")
            except Exception:
                continue
            if text and text.strip():
                return text.splitlines(), ("previous" if previous else "current")
        return [], None

    def _warning_events(
        self, kind, name, namespace, pods_raw, all_events=None
    ) -> list[EventSummary]:
        if all_events is None:
            all_events = self.events.get(namespace)
        groups: dict[tuple[str, str, str], EventSummary] = {}

        # dedup: for a bare Pod the workload target equals its pod target.
        targets = dict.fromkeys(
            [(name, kind)] + [(pod.metadata.name, Kind.Pod) for pod in pods_raw]
        )
        for obj_name, obj_kind in targets:
            for event in self.events.filter(all_events, obj_name, obj_kind):
                if getattr(event, "type", None) != "Warning":
                    continue
                obj = event.involved_object
                key = (event.reason, obj.kind, obj.name)
                count = getattr(event, "count", None) or 1
                timestamp = getattr(event, "last_timestamp", None) or getattr(
                    event.metadata, "creation_timestamp", None
                )
                existing = groups.get(key)
                if existing is None:
                    groups[key] = EventSummary(
                        reason=event.reason,
                        message=event.message,
                        kind=obj.kind,
                        name=obj.name,
                        count=count,
                        last_timestamp=timestamp,
                    )
                else:
                    existing.count += count
                    existing.message = event.message
                    if timestamp:
                        existing.last_timestamp = timestamp
        return list(groups.values())


def _int(value) -> int:
    """The SDK returns None (not 0) for zero counters."""
    return value or 0


def _replica_health_from(kind, obj) -> ReplicaHealth | None:
    """Extract replica health from an already-fetched workload object."""
    status = obj.status
    if kind in (Kind.Deployment, Kind.StatefulSet):
        return ReplicaHealth(
            desired=_int(obj.spec.replicas),
            ready=_int(status.ready_replicas),
            available=_int(getattr(status, "available_replicas", None)),
            updated=_int(status.updated_replicas),
            generation=obj.metadata.generation,
            observed_generation=status.observed_generation,
        )
    if kind == Kind.DaemonSet:
        return ReplicaHealth(
            desired=_int(status.desired_number_scheduled),
            ready=_int(status.number_ready),
            available=_int(status.number_available),
            updated=_int(status.updated_number_scheduled),
            generation=obj.metadata.generation,
            observed_generation=status.observed_generation,
        )
    return None


def _job_health_from(obj) -> JobHealth:
    """Extract job health from an already-fetched Job object."""
    status = obj.status
    conditions = getattr(status, "conditions", None) or []
    failed_reasons = {
        c.reason for c in conditions if getattr(c, "type", None) == "Failed"
    }
    return JobHealth(
        succeeded=_int(status.succeeded),
        failed=_int(status.failed),
        active=_int(status.active),
        suspended=bool(obj.spec.suspend),
        backoff_limit=_int(obj.spec.backoff_limit),
        backoff_limit_exceeded="BackoffLimitExceeded" in failed_reasons,
        deadline_exceeded="DeadlineExceeded" in failed_reasons,
    )


def _matches_selector(pod, selector: dict) -> bool:
    labels = getattr(pod.metadata, "labels", None) or {}
    return all(labels.get(key) == value for key, value in selector.items())


def _service_health_from(svc, endpoints) -> ServiceHealth:
    """Extract service health from an already-fetched Service and its
    Endpoints object (None if the Endpoints read failed/404ed)."""
    ready = 0
    not_ready = 0
    for subset in getattr(endpoints, "subsets", None) or []:
        ready += len(getattr(subset, "addresses", None) or [])
        not_ready += len(getattr(subset, "not_ready_addresses", None) or [])
    return ServiceHealth(
        has_selector=bool(svc.spec.selector),
        ready_addresses=ready,
        not_ready_addresses=not_ready,
    )


def _container_needs_logs(container: ContainerDiagnostic) -> bool:
    """Any container that is unhealthy in some way: not ready, not currently
    running, restarted, or previously terminated. Fully healthy containers are
    skipped."""
    return (
        not container.ready
        or container.state != "Running"
        or container.restart_count > 0
        or container.last_terminated_reason is not None
    )


def _pod_diagnostic(pod) -> PodDiagnostic:
    status = pod.status
    statuses = getattr(status, "container_statuses", None) or []
    containers = [_container_diagnostic(cs) for cs in statuses]
    return PodDiagnostic(
        name=pod.metadata.name,
        phase=getattr(status, "phase", None) or "Unknown",
        node=getattr(pod.spec, "node_name", None),
        ready_containers=sum(1 for cs in statuses if cs.ready),
        total_containers=len(statuses),
        containers=containers,
        scheduling=_scheduling_info(status),
    )


def _container_diagnostic(cs) -> ContainerDiagnostic:
    state = cs.state
    waiting = getattr(state, "waiting", None)
    running = getattr(state, "running", None)
    terminated = getattr(state, "terminated", None)

    if running is not None:
        state_str = "Running"
    elif waiting is not None:
        state_str = "Waiting"
    elif terminated is not None:
        state_str = "Terminated"
    else:
        state_str = "Unknown"

    last_terminated = getattr(getattr(cs, "last_state", None), "terminated", None)

    return ContainerDiagnostic(
        name=cs.name,
        ready=cs.ready,
        started=getattr(cs, "started", None),
        restart_count=cs.restart_count,
        state=state_str,
        waiting_reason=getattr(waiting, "reason", None) if waiting else None,
        waiting_message=getattr(waiting, "message", None) if waiting else None,
        terminated_reason=getattr(terminated, "reason", None) if terminated else None,
        exit_code=getattr(terminated, "exit_code", None) if terminated else None,
        last_terminated_reason=(
            getattr(last_terminated, "reason", None) if last_terminated else None
        ),
        last_exit_code=(
            getattr(last_terminated, "exit_code", None) if last_terminated else None
        ),
    )


def _scheduling_info(status) -> SchedulingInfo:
    conditions = getattr(status, "conditions", None) or []
    for condition in conditions:
        if condition.type == "PodScheduled" and condition.status != "True":
            return SchedulingInfo(
                schedulable=False,
                reason=getattr(condition, "reason", None),
                message=getattr(condition, "message", None),
            )
    return SchedulingInfo(schedulable=True)
