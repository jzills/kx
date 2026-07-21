"""DiagnosticsService with the kubernetes client mocked. Pins SDK-attribute
extraction (replica counts, container states, scheduling, event dedup)."""

from contextlib import contextmanager
from datetime import datetime, timezone
from decimal import Decimal
from types import SimpleNamespace as NS
from unittest.mock import MagicMock, patch

from kx.diagnostics import DiagnosticsService, filter_severity_lines
from kx.kinds import Kind


# --- fake kubernetes objects ------------------------------------------------


def _meta(
    name, uid="", owners=(), generation=None, labels=None, creation_timestamp=None
):
    return NS(
        name=name,
        uid=uid,
        owner_references=[NS(uid=o) for o in owners],
        generation=generation,
        labels=labels or {},
        creation_timestamp=creation_timestamp,
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


def _spec_container(name="app", limits=None):
    return NS(name=name, resources=NS(limits=limits))


def _pod(
    name,
    uid="",
    owner=None,
    phase="Running",
    statuses=None,
    conditions=None,
    labels=None,
    spec_containers=None,
):
    owners = (owner,) if owner else ()
    statuses = statuses if statuses is not None else [_cstatus()]
    if spec_containers is None:
        spec_containers = [_spec_container(name=cs.name) for cs in statuses]
    return NS(
        metadata=_meta(name, uid, owners, labels=labels),
        spec=NS(node_name="node-1", containers=spec_containers),
        status=NS(
            phase=phase,
            container_statuses=statuses,
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
def _mocked(apps=None, core=None, batch=None):
    client = MagicMock()
    client.AppsV1Api.return_value = apps or MagicMock()
    client.CoreV1Api.return_value = core or MagicMock()
    client.BatchV1Api.return_value = batch or MagicMock()
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

    def test_container_limits_extracted_from_pod_spec(self):
        core = MagicMock()
        core.read_namespaced_pod.return_value = _pod(
            "solo",
            spec_containers=[
                _spec_container("app", limits={"cpu": "500m", "memory": "256Mi"})
            ],
        )
        with _mocked(core=core):
            data = _service().gather(Kind.Pod, "solo", "default")
        container = data.pods[0].containers[0]
        assert container.cpu_limit == Decimal("0.5")
        assert container.memory_limit == Decimal(256 * 1024 * 1024)

    def test_container_with_no_limits_has_none(self):
        core = MagicMock()
        core.read_namespaced_pod.return_value = _pod(
            "solo", spec_containers=[_spec_container("app", limits=None)]
        )
        with _mocked(core=core):
            data = _service().gather(Kind.Pod, "solo", "default")
        container = data.pods[0].containers[0]
        assert container.cpu_limit is None
        assert container.memory_limit is None

    def test_container_with_only_memory_limit(self):
        core = MagicMock()
        core.read_namespaced_pod.return_value = _pod(
            "solo",
            spec_containers=[_spec_container("app", limits={"memory": "128Mi"})],
        )
        with _mocked(core=core):
            data = _service().gather(Kind.Pod, "solo", "default")
        container = data.pods[0].containers[0]
        assert container.cpu_limit is None
        assert container.memory_limit == Decimal(128 * 1024 * 1024)

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


def _job(
    name,
    uid="",
    succeeded=0,
    failed=0,
    active=0,
    suspended=False,
    backoff_limit=6,
    conditions=(),
    owner=None,
    created=None,
):
    owners = (owner,) if owner else ()
    return NS(
        metadata=_meta(name, uid, owners, creation_timestamp=created),
        spec=NS(suspend=suspended, backoff_limit=backoff_limit),
        status=NS(
            succeeded=succeeded,
            failed=failed,
            active=active,
            conditions=list(conditions),
        ),
    )


def _condition(cond_type, reason=None):
    return NS(type=cond_type, reason=reason)


class TestJobHealth:
    def test_active_job_has_no_failure_flags(self):
        apps = MagicMock()
        batch = MagicMock()
        batch.read_namespaced_job.return_value = _job("nightly", "jU", active=1)
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items()
        with _mocked(apps=apps, core=core, batch=batch):
            data = _service().gather(Kind.Job, "nightly", "default")
        assert data.job.active == 1
        assert data.job.backoff_limit_exceeded is False
        assert data.job.deadline_exceeded is False
        assert data.replicas is None

    def test_backoff_limit_exceeded_condition_is_flagged(self):
        apps = MagicMock()
        batch = MagicMock()
        batch.read_namespaced_job.return_value = _job(
            "nightly",
            "jU",
            failed=3,
            backoff_limit=3,
            conditions=[_condition("Failed", "BackoffLimitExceeded")],
        )
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items()
        with _mocked(apps=apps, core=core, batch=batch):
            data = _service().gather(Kind.Job, "nightly", "default")
        assert data.job.backoff_limit_exceeded is True
        assert data.job.failed == 3
        assert data.job.backoff_limit == 3

    def test_deadline_exceeded_condition_is_flagged(self):
        apps = MagicMock()
        batch = MagicMock()
        batch.read_namespaced_job.return_value = _job(
            "nightly", "jU", conditions=[_condition("Failed", "DeadlineExceeded")]
        )
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items()
        with _mocked(apps=apps, core=core, batch=batch):
            data = _service().gather(Kind.Job, "nightly", "default")
        assert data.job.deadline_exceeded is True
        assert data.job.backoff_limit_exceeded is False

    def test_suspended_job_is_extracted(self):
        apps = MagicMock()
        batch = MagicMock()
        batch.read_namespaced_job.return_value = _job("nightly", "jU", suspended=True)
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items()
        with _mocked(apps=apps, core=core, batch=batch):
            data = _service().gather(Kind.Job, "nightly", "default")
        assert data.job.suspended is True

    def test_completed_job_reports_succeeded_count(self):
        apps = MagicMock()
        batch = MagicMock()
        batch.read_namespaced_job.return_value = _job(
            "backup", "jU", succeeded=1, conditions=[_condition("Complete")]
        )
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items()
        with _mocked(apps=apps, core=core, batch=batch):
            data = _service().gather(Kind.Job, "backup", "default")
        assert data.job.succeeded == 1
        assert data.job.backoff_limit_exceeded is False
        assert data.job.deadline_exceeded is False

    def test_job_pods_resolved_by_owner_uid(self):
        apps = MagicMock()
        batch = MagicMock()
        batch.read_namespaced_job.return_value = _job("nightly", "jU")
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items(
            _pod("nightly-x1", "p1", owner="jU"),
            _pod("other", "p2", owner="unrelated"),
        )
        with _mocked(apps=apps, core=core, batch=batch):
            data = _service().gather(Kind.Job, "nightly", "default")
        assert [p.name for p in data.pods] == ["nightly-x1"]


def _svc(name, uid="", selector=None):
    sel = {"app": name} if selector is None else selector
    return NS(metadata=_meta(name, uid), spec=NS(selector=sel))


def _endpoint_subset(ready=0, not_ready=0):
    return NS(
        addresses=[NS() for _ in range(ready)],
        not_ready_addresses=[NS() for _ in range(not_ready)],
    )


def _endpoints(*subsets):
    return NS(subsets=list(subsets))


def _endpoints_named(name, *subsets):
    return NS(metadata=_meta(name), subsets=list(subsets))


class TestServiceHealth:
    def test_healthy_service_has_all_ready_addresses(self):
        core = MagicMock()
        core.read_namespaced_service.return_value = _svc("web", "svcU")
        core.read_namespaced_endpoints.return_value = _endpoints(
            _endpoint_subset(ready=2)
        )
        core.list_namespaced_pod.return_value = _items()
        with _mocked(core=core):
            data = _service().gather(Kind.Service, "web", "default")
        assert data.service.has_selector is True
        assert data.service.ready_addresses == 2
        assert data.service.not_ready_addresses == 0

    def test_service_without_selector_is_extracted(self):
        core = MagicMock()
        core.read_namespaced_service.return_value = _svc("ext", "svcU", selector={})
        core.read_namespaced_endpoints.return_value = _endpoints()
        core.list_namespaced_pod.return_value = _items()
        with _mocked(core=core):
            data = _service().gather(Kind.Service, "ext", "default")
        assert data.service.has_selector is False

    def test_missing_endpoints_object_yields_zero_addresses(self):
        core = MagicMock()
        core.read_namespaced_service.return_value = _svc("web", "svcU")
        core.read_namespaced_endpoints.side_effect = Exception("not found")
        core.list_namespaced_pod.return_value = _items()
        with _mocked(core=core):
            data = _service().gather(Kind.Service, "web", "default")
        assert data.service.ready_addresses == 0
        assert data.service.not_ready_addresses == 0

    def test_service_pods_resolved_via_selector(self):
        core = MagicMock()
        core.read_namespaced_service.return_value = _svc("web", "svcU", {"app": "web"})
        core.read_namespaced_endpoints.return_value = _endpoints()
        core.list_namespaced_pod.return_value = _items(_pod("web-1", "p1"))
        with _mocked(core=core):
            data = _service().gather(Kind.Service, "web", "default")
        assert [p.name for p in data.pods] == ["web-1"]
        args, kwargs = core.list_namespaced_pod.call_args
        assert kwargs.get("label_selector") == "app=web"

    def test_no_selector_resolves_no_pods(self):
        core = MagicMock()
        core.read_namespaced_service.return_value = _svc("ext", "svcU", selector={})
        core.read_namespaced_endpoints.return_value = _endpoints()
        with _mocked(core=core):
            data = _service().gather(Kind.Service, "ext", "default")
        assert data.pods == []
        core.list_namespaced_pod.assert_not_called()


def _pvc(name, uid="", phase="Bound"):
    return NS(metadata=_meta(name, uid), status=NS(phase=phase))


class TestPvcHealth:
    def test_bound_pvc_is_extracted(self):
        core = MagicMock()
        core.read_namespaced_persistent_volume_claim.return_value = _pvc(
            "data", "pvcU", phase="Bound"
        )
        with _mocked(core=core):
            data = _service().gather(Kind.PersistentVolumeClaim, "data", "default")
        assert data.pvc.phase == "Bound"
        assert data.pods == []
        assert data.replicas is None

    def test_pending_pvc_is_extracted(self):
        core = MagicMock()
        core.read_namespaced_persistent_volume_claim.return_value = _pvc(
            "data", "pvcU", phase="Pending"
        )
        with _mocked(core=core):
            data = _service().gather(Kind.PersistentVolumeClaim, "data", "default")
        assert data.pvc.phase == "Pending"

    def test_pvc_warning_events_flow_through_generic_path(self):
        events = MagicMock()
        events.get.return_value = [_event("Warning", "ProvisioningFailed", "data")]
        events.filter.side_effect = lambda evs, name, kind: [
            e for e in evs if e.involved_object.name == name
        ]
        core = MagicMock()
        core.read_namespaced_persistent_volume_claim.return_value = _pvc(
            "data", "pvcU", phase="Pending"
        )
        with _mocked(core=core):
            data = _service(events=events).gather(
                Kind.PersistentVolumeClaim, "data", "default"
            )
        assert [e.reason for e in data.warning_events] == ["ProvisioningFailed"]


def _cronjob(name, uid="", suspended=False):
    return NS(metadata=_meta(name, uid), spec=NS(suspend=suspended))


class TestCronJobHealth:
    def test_suspended_cronjob_is_extracted(self):
        batch = MagicMock()
        batch.read_namespaced_cron_job.return_value = _cronjob(
            "nightly", "cjU", suspended=True
        )
        batch.list_namespaced_job.return_value = _items()
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items()
        with _mocked(core=core, batch=batch):
            data = _service().gather(Kind.CronJob, "nightly", "default")
        assert data.cronjob.suspended is True
        assert data.cronjob.most_recent_job is None

    def test_never_run_cronjob_has_no_most_recent_job(self):
        batch = MagicMock()
        batch.read_namespaced_cron_job.return_value = _cronjob("nightly", "cjU")
        batch.list_namespaced_job.return_value = _items()
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items()
        with _mocked(core=core, batch=batch):
            data = _service().gather(Kind.CronJob, "nightly", "default")
        assert data.cronjob.most_recent_job is None
        assert data.pods == []

    def test_most_recent_job_by_creation_timestamp_is_used(self):
        batch = MagicMock()
        batch.read_namespaced_cron_job.return_value = _cronjob("nightly", "cjU")
        batch.list_namespaced_job.return_value = _items(
            _job(
                "nightly-old",
                "jOld",
                owner="cjU",
                created=datetime(2026, 7, 1, tzinfo=timezone.utc),
                succeeded=1,
            ),
            _job(
                "nightly-new",
                "jNew",
                owner="cjU",
                created=datetime(2026, 7, 20, tzinfo=timezone.utc),
                failed=3,
                backoff_limit=3,
                conditions=[_condition("Failed", "BackoffLimitExceeded")],
            ),
            _job("unrelated", "jOther", owner="somethingElse"),
        )
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items(
            _pod("nightly-new-x1", "p1", owner="jNew"),
            _pod("nightly-old-x1", "p2", owner="jOld"),
        )
        with _mocked(core=core, batch=batch):
            data = _service().gather(Kind.CronJob, "nightly", "default")
        assert data.cronjob.most_recent_job.backoff_limit_exceeded is True
        assert [p.name for p in data.pods] == ["nightly-new-x1"]


# --- namespace sweep ---------------------------------------------------------


def _deploy(name, uid, ready=1, desired=1):
    return NS(
        metadata=_meta(name, uid, generation=1),
        spec=NS(replicas=desired),
        status=NS(
            ready_replicas=ready,
            available_replicas=ready,
            updated_replicas=ready,
            observed_generation=1,
        ),
    )


def _sweep_sts(name, uid, ready=1, desired=1):
    # Not named _sts: a later helper of that name (MagicMock apps for the
    # log-attachment tests) would shadow it at runtime.
    return NS(
        metadata=_meta(name, uid, generation=1),
        spec=NS(replicas=desired),
        status=NS(
            ready_replicas=ready,
            available_replicas=ready,
            updated_replicas=ready,
            observed_generation=1,
        ),
    )


def _ds(name, uid, ready=1, desired=1):
    return NS(
        metadata=_meta(name, uid, generation=1),
        status=NS(
            desired_number_scheduled=desired,
            number_ready=ready,
            number_available=ready,
            updated_number_scheduled=desired,
            observed_generation=1,
        ),
    )


def _sweep_apps(deploys=(), stss=(), dss=(), replica_sets=()):
    apps = MagicMock()
    apps.list_namespaced_deployment.return_value = _items(*deploys)
    apps.list_namespaced_stateful_set.return_value = _items(*stss)
    apps.list_namespaced_daemon_set.return_value = _items(*dss)
    apps.list_namespaced_replica_set.return_value = _items(*replica_sets)
    return apps


def _sweep_batch(jobs=(), cronjobs=()):
    batch = MagicMock()
    batch.list_namespaced_job.return_value = _items(*jobs)
    batch.list_namespaced_cron_job.return_value = _items(*cronjobs)
    return batch


class TestSweepUsage:
    def test_usage_attached_once_across_workload_and_orphan_pods(self):
        apps = _sweep_apps(
            deploys=[_deploy("web", "dU")],
            replica_sets=[_res("web-abc", "rsU", owner="dU")],
        )
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items(
            _pod("web-1", "p1", owner="rsU"),
            _pod("solo", "p2"),
        )
        metrics = _metrics(
            _metrics_item("web-1", ("app", "50m", "100Mi")),
            _metrics_item("solo", ("app", "10m", "20Mi")),
        )
        with (
            _mocked(apps=apps, core=core),
            patch("kx.diagnostics.get_pods_metrics", return_value=metrics) as mock_gpm,
        ):
            results = _service().sweep("default")
        mock_gpm.assert_called_once()
        web, solo = results
        assert web.pods[0].containers[0].cpu_usage == Decimal("0.05")
        assert solo.pods[0].containers[0].cpu_usage == Decimal("0.01")


class TestSweep:
    def test_orders_workloads_then_orphan_pods(self):
        apps = _sweep_apps(
            deploys=[_deploy("web", "dU")],
            stss=[_sweep_sts("db", "sU")],
            dss=[_ds("agent", "aU")],
            replica_sets=[_res("web-abc", "rsU", owner="dU")],
        )
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items(
            _pod("job-pod-1", "p4", owner="jobU"),
            _pod("web-1", "p1", owner="rsU"),
            _pod("db-0", "p2", owner="sU"),
            _pod("agent-x", "p3", owner="aU"),
            _pod("solo", "p5"),
        )
        with _mocked(apps=apps, core=core):
            results = _service().sweep("default")
        assert [(d.kind, d.name) for d in results] == [
            (Kind.Deployment, "web"),
            (Kind.StatefulSet, "db"),
            (Kind.DaemonSet, "agent"),
            (Kind.Pod, "job-pod-1"),
            (Kind.Pod, "solo"),
        ]

    def test_workload_data_carries_owned_pods_and_replicas(self):
        apps = _sweep_apps(
            deploys=[_deploy("web", "dU", ready=1, desired=3)],
            replica_sets=[_res("web-abc", "rsU", owner="dU")],
        )
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items(
            _pod("web-1", "p1", owner="rsU"),
            _pod("web-2", "p2", owner="rsU"),
        )
        with _mocked(apps=apps, core=core):
            results = _service().sweep("default")
        assert len(results) == 1
        data = results[0]
        assert [p.name for p in data.pods] == ["web-1", "web-2"]
        assert data.replicas.desired == 3
        assert data.replicas.ready == 1
        assert data.namespace == "default"
        # Replica health comes from the listed object, not a per-resource read.
        apps.read_namespaced_deployment.assert_not_called()

    def test_orphan_pod_has_no_replica_health(self):
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items(_pod("solo", "p1"))
        with _mocked(apps=_sweep_apps(), core=core):
            results = _service().sweep("default")
        assert results[0].replicas is None
        assert [p.name for p in results[0].pods] == ["solo"]

    def test_sweep_never_fetches_logs(self):
        crashing = _cstatus(
            ready=False,
            restart_count=7,
            state=_cstate(waiting=NS(reason="CrashLoopBackOff", message=None)),
        )
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items(
            _pod("solo", "p1", statuses=[crashing])
        )
        with _mocked(apps=_sweep_apps(), core=core):
            _service().sweep("default")
        core.read_namespaced_pod_log.assert_not_called()

    def test_sweep_fetches_events_once_and_partitions(self):
        events = MagicMock()
        events.get.return_value = [
            _event("Warning", "BackOff", "web-1"),
            _event("Warning", "FailedScheduling", "solo"),
        ]
        events.filter.side_effect = lambda evs, name, kind: [
            e for e in evs if e.involved_object.name == name
        ]
        apps = _sweep_apps(
            deploys=[_deploy("web", "dU")],
            replica_sets=[_res("web-abc", "rsU", owner="dU")],
        )
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items(
            _pod("web-1", "p1", owner="rsU"),
            _pod("solo", "p2"),
        )
        with _mocked(apps=apps, core=core):
            results = _service(events=events).sweep("default")
        events.get.assert_called_once_with("default")
        web, solo = results
        assert [e.name for e in web.warning_events] == ["web-1"]
        assert [e.name for e in solo.warning_events] == ["solo"]

    def test_sweeps_jobs_and_excludes_their_pods_from_orphans(self):
        apps = _sweep_apps()
        batch = _sweep_batch(jobs=[_job("nightly", "jU", succeeded=1)])
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items(
            _pod("nightly-x1", "p1", owner="jU"),
            _pod("solo", "p2"),
        )
        with _mocked(apps=apps, core=core, batch=batch):
            results = _service().sweep("default")
        assert [(d.kind, d.name) for d in results] == [
            (Kind.Job, "nightly"),
            (Kind.Pod, "solo"),
        ]
        job_result = results[0]
        assert [p.name for p in job_result.pods] == ["nightly-x1"]
        assert job_result.job.succeeded == 1
        assert job_result.replicas is None

    def test_sweep_includes_completed_job_as_healthy(self):
        apps = _sweep_apps()
        batch = _sweep_batch(
            jobs=[
                _job("backup", "jU", succeeded=1, conditions=[_condition("Complete")])
            ]
        )
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items()
        with _mocked(apps=apps, core=core, batch=batch):
            results = _service().sweep("default")
        assert results[0].job.backoff_limit_exceeded is False
        assert results[0].job.deadline_exceeded is False

    def test_sweeps_services_without_claiming_their_pods_from_orphans(self):
        apps = _sweep_apps()
        core = MagicMock()
        core.list_namespaced_service.return_value = _items(
            _svc("web-svc", "svcU", selector={"app": "web"})
        )
        core.list_namespaced_endpoints.return_value = _items(
            _endpoints_named("web-svc", _endpoint_subset(ready=1))
        )
        core.list_namespaced_pod.return_value = _items(
            _pod("web-1", "p1", labels={"app": "web"})
        )
        with _mocked(apps=apps, core=core):
            results = _service().sweep("default")
        assert [(d.kind, d.name) for d in results] == [
            (Kind.Service, "web-svc"),
            (Kind.Pod, "web-1"),
        ]
        svc_result = results[0]
        assert [p.name for p in svc_result.pods] == ["web-1"]
        assert svc_result.service.ready_addresses == 1

    def test_sweep_service_missing_endpoints_entry_is_zero_addresses(self):
        apps = _sweep_apps()
        core = MagicMock()
        core.list_namespaced_service.return_value = _items(
            _svc("orphaned-svc", "svcU", selector={"app": "gone"})
        )
        core.list_namespaced_endpoints.return_value = _items()
        core.list_namespaced_pod.return_value = _items()
        with _mocked(apps=apps, core=core):
            results = _service().sweep("default")
        assert results[0].service.ready_addresses == 0
        assert results[0].service.not_ready_addresses == 0

    def test_sweeps_pvcs(self):
        apps = _sweep_apps()
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items()
        core.list_namespaced_persistent_volume_claim.return_value = _items(
            _pvc("data", "pvcU", phase="Pending")
        )
        with _mocked(apps=apps, core=core):
            results = _service().sweep("default")
        assert [(d.kind, d.name) for d in results] == [
            (Kind.PersistentVolumeClaim, "data")
        ]
        assert results[0].pvc.phase == "Pending"

    def test_sweep_cronjob_excludes_its_owned_job_from_standalone_rows(self):
        apps = _sweep_apps()
        batch = _sweep_batch(
            jobs=[
                _job(
                    "nightly-abc",
                    "jU",
                    owner="cjU",
                    created=datetime(2026, 7, 20, tzinfo=timezone.utc),
                    succeeded=1,
                ),
                _job("standalone", "jStandalone", succeeded=1),
            ],
            cronjobs=[_cronjob("nightly", "cjU")],
        )
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items()
        with _mocked(apps=apps, core=core, batch=batch):
            results = _service().sweep("default")
        assert [(d.kind, d.name) for d in results] == [
            (Kind.CronJob, "nightly"),
            (Kind.Job, "standalone"),
        ]
        assert results[0].cronjob.most_recent_job.succeeded == 1

    def test_sweep_cronjob_owned_job_pods_are_not_orphaned(self):
        # A pod belonging to a CronJob-owned Job must not leak through as a
        # standalone orphan-pod row just because its Job was excluded from
        # the standalone Job listing.
        apps = _sweep_apps()
        batch = _sweep_batch(
            jobs=[
                _job(
                    "nightly-abc",
                    "jU",
                    owner="cjU",
                    created=datetime(2026, 7, 20, tzinfo=timezone.utc),
                    failed=3,
                    backoff_limit=3,
                    conditions=[_condition("Failed", "BackoffLimitExceeded")],
                )
            ],
            cronjobs=[_cronjob("nightly", "cjU")],
        )
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items(
            _pod("nightly-abc-x1", "p1", owner="jU")
        )
        with _mocked(apps=apps, core=core, batch=batch):
            results = _service().sweep("default")
        assert [(d.kind, d.name) for d in results] == [(Kind.CronJob, "nightly")]
        assert [p.name for p in results[0].pods] == ["nightly-abc-x1"]

    def test_sweep_cronjob_with_failed_most_recent_job(self):
        apps = _sweep_apps()
        batch = _sweep_batch(
            jobs=[
                _job(
                    "nightly-abc",
                    "jU",
                    owner="cjU",
                    created=datetime(2026, 7, 20, tzinfo=timezone.utc),
                    failed=3,
                    backoff_limit=3,
                    conditions=[_condition("Failed", "BackoffLimitExceeded")],
                )
            ],
            cronjobs=[_cronjob("nightly", "cjU")],
        )
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items()
        with _mocked(apps=apps, core=core, batch=batch):
            results = _service().sweep("default")
        assert results[0].cronjob.most_recent_job.backoff_limit_exceeded is True

    def test_empty_namespace_returns_no_results(self):
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items()
        with _mocked(apps=_sweep_apps(), core=core):
            assert _service().sweep("default") == []


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


def _metrics(*items):
    return {"items": list(items)}


def _metrics_item(pod_name, *container_usages):
    return {
        "metadata": {"name": pod_name},
        "containers": [
            {"name": name, "usage": {"cpu": cpu, "memory": memory}}
            for name, cpu, memory in container_usages
        ],
    }


class TestUsageAttachment:
    def test_usage_matched_by_pod_and_container_name(self):
        core = MagicMock()
        core.read_namespaced_pod.return_value = _pod("solo")
        with (
            _mocked(core=core),
            patch(
                "kx.diagnostics.get_pods_metrics",
                return_value=_metrics(_metrics_item("solo", ("app", "93m", "188Mi"))),
            ),
        ):
            data = _service().gather(Kind.Pod, "solo", "default")
        container = data.pods[0].containers[0]
        assert container.cpu_usage == Decimal("0.093")
        assert container.memory_usage == Decimal(188 * 1024 * 1024)

    def test_no_matching_metrics_leaves_usage_none(self):
        core = MagicMock()
        core.read_namespaced_pod.return_value = _pod("solo")
        with (
            _mocked(core=core),
            patch("kx.diagnostics.get_pods_metrics", return_value=_metrics()),
        ):
            data = _service().gather(Kind.Pod, "solo", "default")
        container = data.pods[0].containers[0]
        assert container.cpu_usage is None
        assert container.memory_usage is None

    def test_metrics_api_failure_is_swallowed(self):
        core = MagicMock()
        core.read_namespaced_pod.return_value = _pod("solo")
        with (
            _mocked(core=core),
            patch(
                "kx.diagnostics.get_pods_metrics", side_effect=Exception("no metrics")
            ),
        ):
            data = _service().gather(Kind.Pod, "solo", "default")
        container = data.pods[0].containers[0]
        assert container.cpu_usage is None
        assert container.memory_usage is None


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
