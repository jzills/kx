import re
from dataclasses import dataclass, field
from datetime import datetime
from enum import IntEnum
from typing import Protocol

from kubernetes import client

from kx.events import EventsServiceProtocol
from kx.graph import resolve_workload_pods
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
    pods: list[PodDiagnostic] = field(default_factory=list)
    warning_events: list[EventSummary] = field(default_factory=list)


@dataclass
class Finding:
    severity: Severity
    summary: str


@dataclass
class DiagnosticReport:
    """Analysed report produced by the command from DiagnosticData."""

    kind: str
    name: str
    namespace: str
    verdict: Severity  # == max finding severity (OK if none)
    findings: list[Finding]  # sorted severity desc, stable
    replicas: ReplicaHealth | None
    pods: list[PodDiagnostic] = field(default_factory=list)
    warning_events: list[EventSummary] = field(default_factory=list)


# --- service ----------------------------------------------------------------


class DiagnosticsServiceProtocol(Protocol):
    def gather(self, kind: str, name: str, namespace: str) -> DiagnosticData: ...


class DiagnosticsService:
    def __init__(self, events: EventsServiceProtocol):
        self.events = events

    def gather(self, kind: str, name: str, namespace: str) -> DiagnosticData:
        load_config()
        apps = client.AppsV1Api()
        core = client.CoreV1Api()

        replicas = self._replica_health(kind, name, namespace, apps)
        pods_raw = resolve_workload_pods(kind, name, namespace, apps, core)
        pods = [_pod_diagnostic(pod) for pod in pods_raw]
        self._attach_logs(pods, namespace, core)
        warning_events = self._warning_events(kind, name, namespace, pods_raw)

        return DiagnosticData(
            kind=kind,
            name=name,
            namespace=namespace,
            replicas=replicas,
            pods=pods,
            warning_events=warning_events,
        )

    def _replica_health(self, kind, name, namespace, apps) -> ReplicaHealth | None:
        if kind == Kind.Deployment:
            obj = apps.read_namespaced_deployment(name, namespace)
            status = obj.status
            return ReplicaHealth(
                desired=_int(obj.spec.replicas),
                ready=_int(status.ready_replicas),
                available=_int(status.available_replicas),
                updated=_int(status.updated_replicas),
                generation=obj.metadata.generation,
                observed_generation=status.observed_generation,
            )
        if kind == Kind.StatefulSet:
            obj = apps.read_namespaced_stateful_set(name, namespace)
            status = obj.status
            return ReplicaHealth(
                desired=_int(obj.spec.replicas),
                ready=_int(status.ready_replicas),
                available=_int(getattr(status, "available_replicas", None)),
                updated=_int(status.updated_replicas),
                generation=obj.metadata.generation,
                observed_generation=status.observed_generation,
            )
        if kind == Kind.DaemonSet:
            obj = apps.read_namespaced_daemon_set(name, namespace)
            status = obj.status
            return ReplicaHealth(
                desired=_int(status.desired_number_scheduled),
                ready=_int(status.number_ready),
                available=_int(status.number_available),
                updated=_int(status.updated_number_scheduled),
                generation=obj.metadata.generation,
                observed_generation=status.observed_generation,
            )
        return None

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

    def _warning_events(self, kind, name, namespace, pods_raw) -> list[EventSummary]:
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
