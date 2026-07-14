import io
import pytest
from rich.console import Console
import kx.console as kx_console


@pytest.fixture(autouse=True)
def capture_console():
    buf = io.StringIO()
    original = kx_console._console
    kx_console._console = Console(file=buf, no_color=True, highlight=False)
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


def test_render_diagnostic_healthy_shows_verdict(capture_console):
    from kx.diagnostics import Severity

    kx_console.render_diagnostic(_diag_report(Severity.OK, []))
    out = capture_console.getvalue()
    assert "Healthy" in out
    assert "No issues detected." in out


def test_render_diagnostic_lists_findings_and_verdict(capture_console):
    from kx.diagnostics import Finding, Severity

    report = _diag_report(
        Severity.CRITICAL,
        [Finding(Severity.CRITICAL, "CrashLoopBackOff in pod web-abc (12 restarts)")],
    )
    kx_console.render_diagnostic(report)
    out = capture_console.getvalue()
    assert "Critical" in out
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
