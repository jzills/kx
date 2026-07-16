"""Command wiring + pure finding-detection logic for `kx diagnostic`.
The DiagnosticsService is mocked, so DiagnosticData is built directly."""

import pytest
from unittest.mock import MagicMock

from kx.commands.diagnostic import DiagnosticCommand, build_report
from kx.diagnostics import (
    ContainerDiagnostic,
    DiagnosticData,
    PodDiagnostic,
    ReplicaHealth,
    SchedulingInfo,
    Severity,
)
from kx.kinds import Kind


# --- builders ---------------------------------------------------------------


def _container(name="app", **kwargs):
    defaults = dict(ready=True, started=True, restart_count=0, state="Running")
    defaults.update(kwargs)
    return ContainerDiagnostic(name=name, **defaults)


def _pod(name="web-1", phase="Running", containers=None, schedulable=True, **kwargs):
    containers = containers if containers is not None else [_container()]
    total = len(containers)
    return PodDiagnostic(
        name=name,
        phase=phase,
        node=kwargs.get("node", "node-1"),
        ready_containers=kwargs.get(
            "ready_containers", sum(1 for c in containers if c.ready)
        ),
        total_containers=kwargs.get("total_containers", total),
        containers=containers,
        scheduling=SchedulingInfo(
            schedulable=schedulable,
            reason=kwargs.get("sched_reason"),
            message=kwargs.get("sched_message"),
        ),
    )


def _data(kind=Kind.Deployment, replicas=None, pods=None, warning_events=None):
    return DiagnosticData(
        kind=kind,
        name="web",
        namespace="default",
        replicas=replicas,
        pods=pods or [],
        warning_events=warning_events or [],
    )


def _summaries(text):
    return " || ".join(f.summary for f in text.findings)


# --- command wiring ---------------------------------------------------------


class TestDiagnosticCommand:
    def test_gathers_and_builds_report(self):
        state = MagicMock()
        state.fields.return_value = ("web", "default", Kind.Deployment)
        diagnostics = MagicMock()
        diagnostics.gather.return_value = _data(
            replicas=ReplicaHealth(3, 3, 3, 3), pods=[_pod()]
        )

        report = DiagnosticCommand(state=state, diagnostics=diagnostics).execute(2)

        assert report.verdict == Severity.OK
        state.fields.assert_called_once_with(2)
        diagnostics.gather.assert_called_once_with(Kind.Deployment, "web", "default")

    def test_unsupported_kind_raises_before_gather(self):
        state = MagicMock()
        state.fields.return_value = ("cfg", "default", Kind.ConfigMap)
        diagnostics = MagicMock()

        with pytest.raises(ValueError):
            DiagnosticCommand(state=state, diagnostics=diagnostics).execute(1)

        diagnostics.gather.assert_not_called()


# --- build_report (pure) ----------------------------------------------------


class TestBuildReport:
    def test_healthy_deployment_has_no_findings(self):
        report = build_report(
            _data(replicas=ReplicaHealth(3, 3, 3, 3), pods=[_pod(), _pod("web-2")])
        )
        assert report.findings == []
        assert report.verdict == Severity.OK

    def test_crashloop_is_critical_and_names_pod(self):
        pod = _pod(
            "web-abc",
            containers=[
                _container(
                    ready=False,
                    state="Waiting",
                    waiting_reason="CrashLoopBackOff",
                    restart_count=12,
                )
            ],
        )
        report = build_report(_data(pods=[pod]))
        assert report.verdict == Severity.CRITICAL
        assert "CrashLoopBackOff" in _summaries(report)
        assert "web-abc" in _summaries(report)
        assert "12" in _summaries(report)

    def test_image_pull_failure_is_critical(self):
        pod = _pod(
            "web-abc",
            containers=[
                _container(
                    ready=False, state="Waiting", waiting_reason="ImagePullBackOff"
                )
            ],
        )
        report = build_report(_data(pods=[pod]))
        assert report.verdict == Severity.CRITICAL
        assert "Image pull failure" in _summaries(report)

    def test_stalled_rollout_is_critical(self):
        report = build_report(
            _data(
                replicas=ReplicaHealth(3, 3, 3, 3, generation=5, observed_generation=3),
                pods=[_pod()],
            )
        )
        assert report.verdict == Severity.CRITICAL
        assert "stalled" in _summaries(report)

    def test_zero_ready_replicas_is_critical(self):
        report = build_report(_data(replicas=ReplicaHealth(3, 0, 0, 3)))
        assert report.verdict == Severity.CRITICAL
        assert "0/3 replicas ready" in _summaries(report)

    def test_partial_ready_replicas_is_warning(self):
        report = build_report(_data(replicas=ReplicaHealth(3, 2, 2, 3)))
        assert report.verdict == Severity.WARNING

    def test_unschedulable_pod_carries_message(self):
        pod = _pod(
            "web-abc",
            phase="Pending",
            containers=[],
            schedulable=False,
            sched_reason="Unschedulable",
            sched_message="0/3 nodes are available: insufficient cpu",
        )
        report = build_report(_data(pods=[pod]))
        assert report.verdict == Severity.CRITICAL
        assert "insufficient cpu" in _summaries(report)

    def test_transient_container_creating_is_surfaced_as_warning(self):
        pod = _pod(
            "web-abc",
            containers=[
                _container(
                    ready=False, state="Waiting", waiting_reason="ContainerCreating"
                )
            ],
        )
        report = build_report(_data(pods=[pod]))
        assert report.verdict == Severity.WARNING
        assert "ContainerCreating" in _summaries(report)

    def test_oomkilled_from_last_state_is_critical(self):
        pod = _pod(
            "web-abc",
            containers=[
                _container(
                    ready=False,
                    state="Waiting",
                    waiting_reason="CrashLoopBackOff",
                    last_terminated_reason="OOMKilled",
                    last_exit_code=137,
                )
            ],
        )
        report = build_report(_data(pods=[pod]))
        assert report.verdict == Severity.CRITICAL
        assert "OOMKilled" in _summaries(report)

    def test_high_restart_count_without_failure_is_warning(self):
        pod = _pod("web-abc", containers=[_container(restart_count=7, state="Running")])
        report = build_report(_data(pods=[pod]))
        assert report.verdict == Severity.WARNING
        assert "restarted 7 times" in _summaries(report)

    def test_bare_pod_has_no_replica_findings(self):
        report = build_report(_data(kind=Kind.Pod, replicas=None, pods=[_pod()]))
        assert report.findings == []
        assert report.verdict == Severity.OK

    def test_warning_events_become_warning_findings(self):
        from kx.diagnostics import EventSummary

        report = build_report(
            _data(
                pods=[_pod()],
                warning_events=[
                    EventSummary(
                        reason="BackOff",
                        message="Back-off restarting",
                        kind="Pod",
                        name="web-1",
                        count=8,
                    )
                ],
            )
        )
        assert report.verdict == Severity.WARNING
        assert "BackOff ×8 on Pod/web-1" in _summaries(report)
        # the message belongs to the WARNING EVENTS section, not the summary line
        assert "Back-off restarting" not in _summaries(report)

    def test_findings_sorted_critical_before_warning(self):
        pod = _pod(
            "web-abc",
            containers=[
                _container(
                    ready=False, state="Waiting", waiting_reason="CrashLoopBackOff"
                )
            ],
        )
        report = build_report(_data(replicas=ReplicaHealth(3, 2, 2, 3), pods=[pod]))
        assert report.verdict == Severity.CRITICAL
        assert report.findings[0].severity == Severity.CRITICAL
        assert report.findings[-1].severity == Severity.WARNING
