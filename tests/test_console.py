import io
from datetime import datetime, timedelta, timezone

import pytest
import kx.console as kx_console


@pytest.fixture(autouse=True)
def capture_console():
    buf = io.StringIO()
    original = kx_console._console
    kx_console._console = kx_console._build_console(plain=True, file=buf)
    yield buf
    kx_console._console = original


def test_print_success_outputs_checkmark(capture_console):
    kx_console.print_success("Deleted pods/nginx")
    out = capture_console.getvalue()
    assert "✓" in out
    assert "Deleted pods/nginx" in out


def test_print_error_outputs_x(capture_console):
    kx_console.print_error("something went wrong")
    out = capture_console.getvalue()
    assert "✗" in out
    assert "something went wrong" in out


def test_print_banner_outputs_resource(capture_console):
    kx_console.print_banner("Pod", "nginx-abc123")
    assert "Pod/nginx-abc123" in capture_console.getvalue()


def test_print_banner_includes_extra_when_provided(capture_console):
    kx_console.print_banner("Pod", "nginx-abc123", extra="8080:80")
    assert "· 8080:80" in capture_console.getvalue()


def test_print_banner_includes_namespace(capture_console):
    kx_console.print_banner("Pod", "nginx-abc123", namespace="kube-system")
    assert "Pod/nginx-abc123 · kube-system" in capture_console.getvalue()


def test_print_banner_includes_namespace_and_extra(capture_console):
    kx_console.print_banner(
        "Pod", "nginx-abc123", namespace="kube-system", extra="2 labels"
    )
    assert "Pod/nginx-abc123 · kube-system · 2 labels" in capture_console.getvalue()


def test_print_banner_extra_without_namespace(capture_console):
    kx_console.print_banner("Pod", "nginx-abc123", extra="8080:80")
    assert "Pod/nginx-abc123 · 8080:80" in capture_console.getvalue()


def test_print_raw_outputs_text_verbatim(capture_console):
    kx_console.print_raw('{"items": []}')
    assert '{"items": []}' in capture_console.getvalue()


def test_configure_plain_replaces_console(capture_console):
    saved = kx_console._console
    kx_console.configure(plain=True)
    assert kx_console._console is not saved


INDEXED_OUTPUT = """\
X   NAME           READY   STATUS             AGE
1   nginx-abc      1/1     Running            2d
2   worker-xyz     0/1     CrashLoopBackOff   5m
3   redis-0        1/1     Running            7d"""

INDEXED_SINGLE = """\
X   NAME       READY   STATUS    AGE
1   nginx-abc  1/1     Running   2d"""


def test_render_indexed_table_shows_metadata_header(capture_console):
    kx_console.render_indexed_table(INDEXED_OUTPUT, "pods", "default")
    out = capture_console.getvalue()
    assert "Pods" in out
    assert "default" in out
    assert "3 items" in out


def test_render_indexed_table_singular_item(capture_console):
    kx_console.render_indexed_table(INDEXED_SINGLE, "pods", "default")
    assert "1 item" in capture_console.getvalue()


def test_render_indexed_table_shows_x_header(capture_console):
    kx_console.render_indexed_table(INDEXED_OUTPUT, "pods", "default")
    out = capture_console.getvalue()
    assert "X" in out


def test_render_indexed_table_shows_all_names(capture_console):
    kx_console.render_indexed_table(INDEXED_OUTPUT, "pods", "default")
    out = capture_console.getvalue()
    assert "nginx-abc" in out
    assert "worker-xyz" in out
    assert "redis-0" in out


def test_render_indexed_table_non_tabular_falls_through(capture_console):
    kx_console.render_indexed_table('{"items": []}', "pods", "default")
    assert '{"items": []}' in capture_console.getvalue()


def test_render_indexed_table_empty_string(capture_console):
    kx_console.render_indexed_table("", "pods", "default")
    assert capture_console.getvalue() == ""


EVENTS_OUTPUT = (
    "Normal   Pulling                        Pod        2024-01-01 12:00:00+00:00 Pulling image nginx\n"
    "Warning  BackOff                        Pod        2024-01-01 12:01:00+00:00 Back-off restarting"
)


def test_render_events_table_shows_type_values(capture_console):
    kx_console.render_events_table(EVENTS_OUTPUT)
    out = capture_console.getvalue()
    assert "Normal" in out
    assert "Warning" in out


def test_render_events_table_shows_reason(capture_console):
    kx_console.render_events_table(EVENTS_OUTPUT)
    out = capture_console.getvalue()
    assert "Pulling" in out
    assert "BackOff" in out


def test_render_events_table_shows_message(capture_console):
    kx_console.render_events_table(EVENTS_OUTPUT)
    assert "Back-off restarting" in capture_console.getvalue()


def test_render_events_table_no_events(capture_console):
    kx_console.render_events_table("No events found")
    assert "No events found" in capture_console.getvalue()


STATE_JSON = '{"resources": {"nginx": "Pod", "redis": "Pod"}, "namespace": "staging"}'
STATE_MULTI_KIND = (
    '{"resources": {"nginx": "Pod", "my-app": "Deployment"}, "namespace": "default"}'
)


def test_render_state_shows_namespace_in_header(capture_console):
    kx_console.render_state(STATE_JSON)
    assert "staging" in capture_console.getvalue()


def test_render_state_shows_item_count(capture_console):
    kx_console.render_state(STATE_JSON)
    assert "2 items" in capture_console.getvalue()


def test_render_state_single_kind_pluralized_in_header(capture_console):
    kx_console.render_state(STATE_JSON)
    assert "Pods" in capture_console.getvalue()


def test_render_state_mixed_kinds_shows_mixed_in_header(capture_console):
    kx_console.render_state(STATE_MULTI_KIND)
    assert "Mixed" in capture_console.getvalue()


def test_render_state_shows_table_headers(capture_console):
    kx_console.render_state(STATE_JSON)
    out = capture_console.getvalue()
    assert "X" in out
    assert "KIND" in out
    assert "NAME" in out


def test_render_state_shows_resource_names(capture_console):
    kx_console.render_state(STATE_JSON)
    out = capture_console.getvalue()
    assert "nginx" in out
    assert "redis" in out


def test_render_state_shows_kind(capture_console):
    kx_console.render_state(STATE_JSON)
    assert "Pod" in capture_console.getvalue()


def test_render_state_multi_kind(capture_console):
    kx_console.render_state(STATE_MULTI_KIND)
    out = capture_console.getvalue()
    assert "Deployment" in out
    assert "my-app" in out


def test_render_state_singular_item(capture_console):
    single = '{"resources": {"nginx": "Pod"}, "namespace": "default"}'
    kx_console.render_state(single)
    assert "1 item" in capture_console.getvalue()


def _diag_report(verdict, findings, pods=None, replicas=None, warning_events=None):
    from kx.diagnostics import DiagnosticReport

    return DiagnosticReport(
        kind="Deployment",
        name="web",
        namespace="default",
        verdict=verdict,
        findings=findings,
        replicas=replicas,
        pods=pods or [],
        warning_events=warning_events or [],
    )


def test_render_diagnostic_healthy_reports_no_issues(capture_console):
    from kx.diagnostics import Severity

    kx_console.render_diagnostic(_diag_report(Severity.OK, []))
    out = capture_console.getvalue()
    assert "No issues detected." in out


def test_render_diagnostic_banner_carries_verdict_and_count(capture_console):
    from kx.diagnostics import Finding, Severity

    report = _diag_report(
        Severity.CRITICAL,
        [Finding(Severity.CRITICAL, "boom"), Finding(Severity.WARNING, "hmm")],
    )
    kx_console.render_diagnostic(report)
    out = capture_console.getvalue()
    assert "Deployment/web · default · ✗ Critical · 2 issues" in out
    # the verdict lives in the banner only — no standalone line beneath it
    assert "issues found" not in out
    assert out.count("Critical") == 1


def test_render_diagnostic_banner_uses_singular_issue(capture_console):
    from kx.diagnostics import Finding, Severity

    report = _diag_report(Severity.WARNING, [Finding(Severity.WARNING, "hmm")])
    kx_console.render_diagnostic(report)
    assert (
        "Deployment/web · default · ! Warnings · 1 issue" in capture_console.getvalue()
    )


def test_render_diagnostic_banner_omits_count_when_healthy(capture_console):
    from kx.diagnostics import Severity

    kx_console.render_diagnostic(_diag_report(Severity.OK, []))
    out = capture_console.getvalue()
    assert "Deployment/web · default · ✓ Healthy" in out
    assert "0 issues" not in out


def test_render_diagnostic_lists_findings(capture_console):
    from kx.diagnostics import Finding, Severity

    report = _diag_report(
        Severity.CRITICAL,
        [Finding(Severity.CRITICAL, "CrashLoopBackOff in pod web-abc (12 restarts)")],
    )
    kx_console.render_diagnostic(report)
    out = capture_console.getvalue()
    assert "CrashLoopBackOff in pod web-abc" in out


def test_render_diagnostic_shows_replica_and_pod_detail(capture_console):
    from kx.diagnostics import (
        ContainerDiagnostic,
        PodDiagnostic,
        ReplicaHealth,
        SchedulingInfo,
        Severity,
    )

    pod = PodDiagnostic(
        name="web-1",
        phase="Running",
        node="node-1",
        ready_containers=1,
        total_containers=1,
        containers=[
            ContainerDiagnostic(
                name="app", ready=True, started=True, restart_count=0, state="Running"
            )
        ],
        scheduling=SchedulingInfo(schedulable=True),
    )
    report = _diag_report(
        Severity.OK, [], pods=[pod], replicas=ReplicaHealth(3, 3, 3, 3)
    )
    kx_console.render_diagnostic(report)
    out = capture_console.getvalue()
    assert "Desired 3" in out
    assert "web-1" in out
    assert "No warning events." in out


def test_render_diagnostic_shows_log_excerpt(capture_console):
    from kx.diagnostics import (
        ContainerDiagnostic,
        PodDiagnostic,
        SchedulingInfo,
        Severity,
    )

    container = ContainerDiagnostic(
        name="worker",
        ready=False,
        started=True,
        restart_count=4,
        state="Waiting",
        waiting_reason="CrashLoopBackOff",
        log_lines=["ERROR boot failed [config]", "FATAL exit"],
        log_source="previous",
        log_filtered=True,
    )
    pod = PodDiagnostic(
        name="worker-1",
        phase="Running",
        node="node-1",
        ready_containers=0,
        total_containers=1,
        containers=[container],
        scheduling=SchedulingInfo(schedulable=True),
    )
    report = _diag_report(Severity.CRITICAL, [], pods=[pod])
    kx_console.render_diagnostic(report)
    out = capture_console.getvalue()
    assert "LOGS" in out
    assert "worker-1/worker" in out
    assert "(previous)" not in out
    # markup-bearing log text must survive escaping intact
    assert "ERROR boot failed [config]" in out
    assert "FATAL exit" in out


def test_render_diagnostic_logs_note_on_raw_fallback(capture_console):
    from kx.diagnostics import (
        ContainerDiagnostic,
        PodDiagnostic,
        SchedulingInfo,
        Severity,
    )

    container = ContainerDiagnostic(
        name="app",
        ready=False,
        started=True,
        restart_count=0,
        state="Running",
        log_lines=["GET /healthz 404"],
        log_source="current",
        log_filtered=False,
    )
    pod = PodDiagnostic(
        name="fe-1",
        phase="Running",
        node="node-1",
        ready_containers=0,
        total_containers=1,
        containers=[container],
        scheduling=SchedulingInfo(schedulable=True),
    )
    kx_console.render_diagnostic(_diag_report(Severity.WARNING, [], pods=[pod]))
    out = capture_console.getvalue()
    assert "recent output" in out
    assert "GET /healthz 404" in out


def test_render_diagnostic_findings_hang_indent(capture_console):
    from kx.diagnostics import Finding, Severity

    kx_console._console = kx_console._build_console(
        plain=True, file=capture_console, width=60
    )
    finding = Finding(
        severity=Severity.WARNING,
        summary="BackOff ×452 on Pod/worker-crashloop-bc7cb7b55-r7n8b: "
        "Back-off restarting failed container worker",
    )
    kx_console.render_diagnostic(_diag_report(Severity.WARNING, [finding]))
    lines = [line for line in capture_console.getvalue().splitlines() if line.strip()]
    wrapped = [line for line in lines if line.startswith("    ")]
    # continuation lines align under the summary text, not the icon at column 2
    assert wrapped, "expected the long summary to wrap"
    assert all(not line.startswith("     ") for line in wrapped)


def test_render_diagnostic_finding_summary_escapes_markup(capture_console):
    from kx.diagnostics import Finding, Severity

    finding = Finding(
        severity=Severity.WARNING,
        summary="FailedCreatePodSandBox on Pod/web-1: plugin [istio-cni] failed",
    )
    kx_console.render_diagnostic(_diag_report(Severity.WARNING, [finding]))
    assert "plugin [istio-cni] failed" in capture_console.getvalue()


@pytest.mark.parametrize(
    "delta, expected",
    [
        (timedelta(seconds=5), "5s ago"),
        (timedelta(minutes=3), "3m ago"),
        (timedelta(hours=5), "5h ago"),
        (timedelta(days=2), "2d ago"),
        (timedelta(days=2, hours=3), "2d ago"),
        (timedelta(seconds=-30), "just now"),
    ],
)
def test_format_age_buckets(delta, expected):
    stamp = datetime.now(timezone.utc) - delta
    assert kx_console._format_age(stamp) == expected


def test_format_age_without_timestamp():
    assert kx_console._format_age(None) == ""


def test_render_diagnostic_warning_events_stacked(capture_console):
    from kx.diagnostics import EventSummary, Severity

    event = EventSummary(
        reason="FailedCreatePodSandBox",
        message='failed to setup network for sandbox "4aec" [istio-cni]',
        kind="Pod",
        name="worker-crashloop-bc7cb7b5-x8k2",
        count=2,
        last_timestamp=datetime.now(timezone.utc) - timedelta(minutes=29),
    )
    report = _diag_report(Severity.WARNING, [], warning_events=[event])
    kx_console.render_diagnostic(report)
    out = capture_console.getvalue()
    assert "WARNING EVENTS" in out
    # metadata collapses onto one scannable header line
    assert (
        "FailedCreatePodSandBox · Pod/worker-crashloop-bc7cb7b5-x8k2 · ×2 · 29m ago"
        in out
    )
    # the message renders in full beneath, with markup-bearing text intact
    assert 'failed to setup network for sandbox "4aec" [istio-cni]' in out
    # the old squeezed column is gone
    assert "MESSAGE" not in out


def test_render_diagnostic_warning_event_without_timestamp(capture_console):
    from kx.diagnostics import EventSummary, Severity

    event = EventSummary(
        reason="BackOff",
        message="Back-off restarting failed container",
        kind="Pod",
        name="worker-1",
        count=293,
        last_timestamp=None,
    )
    kx_console.render_diagnostic(
        _diag_report(Severity.WARNING, [], warning_events=[event])
    )
    out = capture_console.getvalue()
    assert "BackOff · Pod/worker-1 · ×293" in out
    assert "ago" not in out


def test_configure_swaps_console_with_theme():
    original = kx_console._console
    try:
        kx_console.configure(theme="dracula")
        style = kx_console._console.get_style("header")
        assert style is not None
    finally:
        kx_console._console = original


def test_render_theme_list_shows_all_themes_and_marks_active(capture_console):
    from kx.themes import THEMES

    kx_console.render_theme_list(active="nord")
    out = capture_console.getvalue()
    for name in THEMES:
        assert name in out
    assert "→" in out
