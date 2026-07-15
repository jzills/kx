"""DiagnosticsService with the kubernetes client mocked. Pins SDK-attribute
extraction (replica counts, container states, scheduling, event dedup)."""

from contextlib import contextmanager
from datetime import datetime, timezone
from types import SimpleNamespace as NS
from unittest.mock import MagicMock, patch

from kx.diagnostics import DiagnosticsService, filter_severity_lines
from kx.kinds import Kind


# --- fake kubernetes objects ------------------------------------------------


def _meta(name, uid="", owners=(), generation=None):
    return NS(
        name=name,
        uid=uid,
        owner_references=[NS(uid=o) for o in owners],
        generation=generation,
    )


def _res(name, uid="", owner=None):
    owners = (owner,) if owner else ()
    return NS(metadata=_meta(name, uid, owners))


def _cstate(running=None, waiting=None, terminated=None):
    return NS(running=running, waiting=waiting, terminated=terminated)


def _cstatus(
    name="app",
    ready=True,
    restart_count=0,
    state=None,
    last_terminated=None,
):
    return NS(
        name=name,
        ready=ready,
        started=True,
        restart_count=restart_count,
        state=state or _cstate(running=NS()),
        last_state=NS(terminated=last_terminated),
    )


def _pod(name, uid="", owner=None, phase="Running", statuses=None, conditions=None):
    owners = (owner,) if owner else ()
    return NS(
        metadata=_meta(name, uid, owners),
        spec=NS(node_name="node-1"),
        status=NS(
            phase=phase,
            container_statuses=statuses if statuses is not None else [_cstatus()],
            conditions=conditions or [],
        ),
    )


def _items(*objs):
    return NS(items=list(objs))


def _log(text):
    """Fake the raw HTTP response returned by read_namespaced_pod_log with
    _preload_content=False (a .data bytes attribute)."""
    return NS(data=text.encode("utf-8"))


@contextmanager
def _mocked(apps=None, core=None):
    client = MagicMock()
    client.AppsV1Api.return_value = apps or MagicMock()
    client.CoreV1Api.return_value = core or MagicMock()
    with patch("kx.diagnostics.client", client), patch("kx.diagnostics.load_config"):
        yield


def _service(events=None):
    ev = events or MagicMock()
    if events is None:
        ev.get.return_value = []
        ev.filter.return_value = []
    return DiagnosticsService(events=ev)


# --- replica health ---------------------------------------------------------


class TestReplicaHealth:
    def test_deployment_replica_counts_and_generation(self):
        apps = MagicMock()
        apps.read_namespaced_deployment.return_value = NS(
            metadata=_meta("web", "dU", generation=4),
            spec=NS(replicas=3),
            status=NS(
                ready_replicas=2,
                available_replicas=2,
                updated_replicas=3,
                observed_generation=4,
            ),
        )
        apps.list_namespaced_replica_set.return_value = _items()
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items()
        with _mocked(apps=apps, core=core):
            data = _service().gather(Kind.Deployment, "web", "default")
        assert data.replicas.desired == 3
        assert data.replicas.ready == 2
        assert data.replicas.generation == 4
        assert data.replicas.observed_generation == 4

    def test_none_counters_default_to_zero(self):
        apps = MagicMock()
        apps.read_namespaced_deployment.return_value = NS(
            metadata=_meta("web", "dU", generation=1),
            spec=NS(replicas=1),
            status=NS(
                ready_replicas=None,
                available_replicas=None,
                updated_replicas=None,
                observed_generation=1,
            ),
        )
        apps.list_namespaced_replica_set.return_value = _items()
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items()
        with _mocked(apps=apps, core=core):
            data = _service().gather(Kind.Deployment, "web", "default")
        assert data.replicas.ready == 0
        assert data.replicas.available == 0

    def test_daemonset_uses_daemon_counter_fields(self):
        apps = MagicMock()
        apps.read_namespaced_daemon_set.return_value = NS(
            metadata=_meta("agent", "dsU", generation=2),
            status=NS(
                desired_number_scheduled=5,
                number_ready=4,
                number_available=4,
                updated_number_scheduled=5,
                observed_generation=2,
            ),
        )
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items()
        with _mocked(apps=apps, core=core):
            data = _service().gather(Kind.DaemonSet, "agent", "default")
        assert data.replicas.desired == 5
        assert data.replicas.ready == 4

    def test_bare_pod_has_no_replicas(self):
        core = MagicMock()
        core.read_namespaced_pod.return_value = _pod("solo")
        with _mocked(core=core):
            data = _service().gather(Kind.Pod, "solo", "default")
        assert data.replicas is None
        assert len(data.pods) == 1
        assert data.pods[0].name == "solo"


# --- pod resolution + container extraction ----------------------------------


class TestPodExtraction:
    def test_deployment_two_hop_pod_resolution(self):
        apps = MagicMock()
        apps.read_namespaced_deployment.return_value = _res("web", "deployU")
        apps.list_namespaced_replica_set.return_value = _items(
            _res("web-abc", "rsU", owner="deployU")
        )
        apps.read_namespaced_deployment.return_value.status = NS(
            ready_replicas=1,
            available_replicas=1,
            updated_replicas=1,
            observed_generation=1,
        )
        apps.read_namespaced_deployment.return_value.spec = NS(replicas=1)
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items(
            _pod("web-1", "p1", owner="rsU"),
            _pod("other", "p2", owner="unrelated"),
        )
        with _mocked(apps=apps, core=core):
            data = _service().gather(Kind.Deployment, "web", "default")
        assert [p.name for p in data.pods] == ["web-1"]

    def test_waiting_container_extraction(self):
        statuses = [
            _cstatus(
                name="app",
                ready=False,
                restart_count=3,
                state=_cstate(
                    waiting=NS(reason="CrashLoopBackOff", message="back-off")
                ),
                last_terminated=NS(reason="OOMKilled", exit_code=137),
            )
        ]
        apps = MagicMock()
        apps.read_namespaced_stateful_set.return_value = NS(
            metadata=_meta("db", "stsU", generation=1),
            spec=NS(replicas=1),
            status=NS(
                ready_replicas=0,
                available_replicas=0,
                updated_replicas=1,
                observed_generation=1,
            ),
        )
        core = MagicMock()
        core.read_namespaced_pod_log.return_value = _log("boot\nfailure\n")
        core.list_namespaced_pod.return_value = _items(
            _pod("db-0", "p0", owner="stsU", statuses=statuses)
        )
        with _mocked(apps=apps, core=core):
            data = _service().gather(Kind.StatefulSet, "db", "default")
        container = data.pods[0].containers[0]
        assert container.state == "Waiting"
        assert container.waiting_reason == "CrashLoopBackOff"
        assert container.last_terminated_reason == "OOMKilled"
        assert container.last_exit_code == 137
        assert data.pods[0].ready_containers == 0
        assert data.pods[0].total_containers == 1

    def test_scheduling_condition_parsed(self):
        conditions = [
            NS(
                type="PodScheduled",
                status="False",
                reason="Unschedulable",
                message="0/3 nodes are available: insufficient cpu",
            )
        ]
        core = MagicMock()
        core.read_namespaced_pod.return_value = _pod(
            "pend", phase="Pending", statuses=[], conditions=conditions
        )
        with _mocked(core=core):
            data = _service().gather(Kind.Pod, "pend", "default")
        sched = data.pods[0].scheduling
        assert sched.schedulable is False
        assert "insufficient cpu" in sched.message


# --- warning events ---------------------------------------------------------


_EVENT_TIME = datetime(2024, 1, 1, tzinfo=timezone.utc)


def _event(type_, reason, name, kind="Pod", message="msg", count=1):
    # the k8s SDK hands back datetimes here, not strings
    return NS(
        type=type_,
        reason=reason,
        message=message,
        count=count,
        last_timestamp=_EVENT_TIME,
        involved_object=NS(kind=kind, name=name),
        metadata=NS(creation_timestamp=_EVENT_TIME),
    )


class TestWarningEvents:
    def test_dedup_and_filter_normal(self):
        warn_a = _event("Warning", "BackOff", "web-1", count=3)
        warn_b = _event("Warning", "BackOff", "web-1", count=5)
        normal = _event("Normal", "Pulled", "web-1")
        events = MagicMock()
        events.get.return_value = [warn_a, warn_b, normal]
        events.filter.side_effect = lambda evs, name, kind: [
            e
            for e in evs
            if e.involved_object.name == name and e.involved_object.kind == kind
        ]

        core = MagicMock()
        core.read_namespaced_pod.return_value = _pod("web-1", statuses=[])
        with _mocked(core=core):
            data = _service(events=events).gather(Kind.Pod, "web-1", "default")

        assert len(data.warning_events) == 1
        summary = data.warning_events[0]
        assert summary.reason == "BackOff"
        assert summary.count == 8  # 3 + 5, deduped


# --- OTEL severity filtering (pure) -----------------------------------------


class TestFilterSeverityLines:
    def test_keeps_only_severity_lines(self):
        lines, matched = filter_severity_lines(
            [
                "INFO starting up",
                "ERROR connection refused",
                "DEBUG retrying",
                "FATAL giving up",
            ]
        )
        assert matched is True
        assert lines == ["ERROR connection refused", "FATAL giving up"]

    def test_caps_to_last_matches(self):
        raw = [f"ERROR line {i}" for i in range(20)]
        lines, matched = filter_severity_lines(raw, max_matches=3)
        assert lines == ["ERROR line 17", "ERROR line 18", "ERROR line 19"]

    def test_falls_back_to_raw_tail_when_nothing_matches(self):
        lines, matched = filter_severity_lines(
            ["one", "two", "three", "four"], fallback=2
        )
        assert matched is False
        assert lines == ["three", "four"]

    def test_matches_are_case_insensitive_and_varied(self):
        raw = ["a warn here", "Traceback (most recent call last):", "panic: boom"]
        lines, matched = filter_severity_lines(raw)
        assert matched is True
        assert lines == raw


# --- log attachment (service) -----------------------------------------------


def _crashloop_statuses():
    return [
        _cstatus(
            name="app",
            ready=False,
            restart_count=4,
            state=_cstate(waiting=NS(reason="CrashLoopBackOff", message="back-off")),
            last_terminated=NS(reason="Error", exit_code=1),
        )
    ]


def _sts(name="db", uid="stsU"):
    apps = MagicMock()
    apps.read_namespaced_stateful_set.return_value = NS(
        metadata=_meta(name, uid, generation=1),
        spec=NS(replicas=1),
        status=NS(
            ready_replicas=0,
            available_replicas=0,
            updated_replicas=1,
            observed_generation=1,
        ),
    )
    return apps


class TestLogAttachment:
    def test_crashloop_fetches_previous_instance_logs(self):
        apps = _sts()
        core = MagicMock()
        core.read_namespaced_pod_log.return_value = _log("boot\nERROR fatal crash\n")
        core.list_namespaced_pod.return_value = _items(
            _pod("db-0", "p0", owner="stsU", statuses=_crashloop_statuses())
        )
        with _mocked(apps=apps, core=core):
            data = _service().gather(Kind.StatefulSet, "db", "default")
        container = data.pods[0].containers[0]
        assert container.log_lines == ["ERROR fatal crash"]
        assert container.log_source == "previous"
        # a waiting/crashed container must ask for the previous instance first
        assert core.read_namespaced_pod_log.call_args.kwargs["previous"] is True

    def test_running_not_ready_fetches_current_logs(self):
        statuses = [
            _cstatus(
                name="app",
                ready=False,
                restart_count=0,
                state=_cstate(running=NS()),
            )
        ]
        apps = _sts()
        core = MagicMock()
        core.read_namespaced_pod_log.return_value = _log(
            "GET /healthz 404\nGET /healthz 404\n"
        )
        core.list_namespaced_pod.return_value = _items(
            _pod("db-0", "p0", owner="stsU", statuses=statuses)
        )
        with _mocked(apps=apps, core=core):
            data = _service().gather(Kind.StatefulSet, "db", "default")
        container = data.pods[0].containers[0]
        assert container.log_source == "current"
        assert container.log_filtered is False  # no severity token → raw fallback
        assert core.read_namespaced_pod_log.call_args.kwargs["previous"] is False

    def test_healthy_container_is_not_queried_for_logs(self):
        core = MagicMock()
        core.read_namespaced_pod.return_value = _pod("solo")  # default: ready+running
        with _mocked(core=core):
            data = _service().gather(Kind.Pod, "solo", "default")
        assert data.pods[0].containers[0].log_lines == []
        core.read_namespaced_pod_log.assert_not_called()

    def test_log_api_error_is_swallowed(self):
        apps = _sts()
        core = MagicMock()
        core.read_namespaced_pod_log.side_effect = Exception("no previous logs")
        core.list_namespaced_pod.return_value = _items(
            _pod("db-0", "p0", owner="stsU", statuses=_crashloop_statuses())
        )
        with _mocked(apps=apps, core=core):
            data = _service().gather(Kind.StatefulSet, "db", "default")
        assert data.pods[0].containers[0].log_lines == []
