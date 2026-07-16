import json
import pytest
from unittest.mock import MagicMock
from kx.commands.logs import LogsCommand
from kx.kinds import Kind
from kx.refresh import StaleResourceError


def _make_command(name="nginx", namespace="default", kind=str(Kind.Pod)):
    state = MagicMock()
    state.fields.return_value = (name, namespace, kind)
    kubectl = MagicMock()
    kubectl.run_interactive.return_value = 0
    return LogsCommand(state=state, kubectl=kubectl), state, kubectl


def _workload_json(labels: dict) -> str:
    return json.dumps({"spec": {"selector": {"matchLabels": labels}}})


def _service_json(labels: dict) -> str:
    return json.dumps({"spec": {"selector": labels}})


class TestLogsCommand:
    # --- pod: unchanged behaviour ---

    def test_basic_logs_no_extra_args(self):
        cmd, _, kubectl = _make_command()
        cmd.execute(1)
        kubectl.run_interactive.assert_called_once_with(
            ["logs", "nginx", "-n", "default"]
        )

    def test_follow_flag(self):
        cmd, _, kubectl = _make_command()
        cmd.execute(1, ["-f"])
        kubectl.run_interactive.assert_called_once_with(
            ["logs", "nginx", "-n", "default", "-f"]
        )

    def test_multiple_flags(self):
        cmd, _, kubectl = _make_command()
        cmd.execute(1, ["-c", "sidecar", "--tail=50"])
        kubectl.run_interactive.assert_called_once_with(
            ["logs", "nginx", "-n", "default", "-c", "sidecar", "--tail=50"]
        )

    def test_returns_none(self):
        cmd, _, _ = _make_command()
        result = cmd.execute(1)
        assert result is None

    def test_uses_state_fields(self):
        cmd, state, _ = _make_command(name="my-pod", namespace="kube-system")
        cmd.execute(3)
        state.fields.assert_called_once_with(3)

    # --- unsupported kinds raise ValueError ---

    def test_unsupported_kind_raises_value_error(self):
        cmd, _, _ = _make_command(kind=str(Kind.ConfigMap))
        with pytest.raises(ValueError, match="not supported"):
            cmd.execute(1)

    def test_cronjob_raises_value_error(self):
        cmd, _, _ = _make_command(kind=str(Kind.CronJob))
        with pytest.raises(ValueError, match="not supported"):
            cmd.execute(1)

    # --- aggregate: Deployment ---

    def test_deployment_fetches_selector_json(self):
        cmd, _, kubectl = _make_command(kind=str(Kind.Deployment))
        kubectl.run.return_value = _workload_json({"app": "nginx"})
        cmd.execute(1)
        kubectl.run.assert_called_once_with(
            ["get", "Deployment", "nginx", "-n", "default", "-o", "json"]
        )

    def test_deployment_uses_label_selector(self):
        cmd, _, kubectl = _make_command(kind=str(Kind.Deployment))
        kubectl.run.return_value = _workload_json({"app": "nginx"})
        cmd.execute(1)
        kubectl.run_interactive.assert_called_once_with(
            ["logs", "-l", "app=nginx", "--prefix=true", "-n", "default"]
        )

    def test_deployment_multiple_labels(self):
        cmd, _, kubectl = _make_command(kind=str(Kind.Deployment))
        kubectl.run.return_value = _workload_json({"app": "nginx", "tier": "frontend"})
        cmd.execute(1)
        selector = kubectl.run_interactive.call_args[0][0][2]
        assert "app=nginx" in selector
        assert "tier=frontend" in selector

    def test_deployment_extra_args_passed_through(self):
        cmd, _, kubectl = _make_command(kind=str(Kind.Deployment))
        kubectl.run.return_value = _workload_json({"app": "nginx"})
        cmd.execute(1, ["-f", "--tail=100"])
        kubectl.run_interactive.assert_called_once_with(
            [
                "logs",
                "-l",
                "app=nginx",
                "--prefix=true",
                "-n",
                "default",
                "-f",
                "--tail=100",
            ]
        )

    def test_deployment_empty_selector_raises(self):
        cmd, _, kubectl = _make_command(kind=str(Kind.Deployment))
        kubectl.run.return_value = _workload_json({})
        with pytest.raises(ValueError, match="no pod selector"):
            cmd.execute(1)

    # --- aggregate: StatefulSet ---

    def test_statefulset_uses_label_selector(self):
        cmd, _, kubectl = _make_command(kind=str(Kind.StatefulSet))
        kubectl.run.return_value = _workload_json({"app": "pg"})
        cmd.execute(1)
        kubectl.run_interactive.assert_called_once_with(
            ["logs", "-l", "app=pg", "--prefix=true", "-n", "default"]
        )

    # --- aggregate: DaemonSet ---

    def test_daemonset_uses_label_selector(self):
        cmd, _, kubectl = _make_command(kind=str(Kind.DaemonSet))
        kubectl.run.return_value = _workload_json({"name": "fluentd"})
        cmd.execute(1)
        kubectl.run_interactive.assert_called_once_with(
            ["logs", "-l", "name=fluentd", "--prefix=true", "-n", "default"]
        )

    # --- aggregate: Service ---

    def test_service_uses_spec_selector(self):
        cmd, _, kubectl = _make_command(kind=str(Kind.Service))
        kubectl.run.return_value = _service_json({"app": "web"})
        cmd.execute(1)
        kubectl.run_interactive.assert_called_once_with(
            ["logs", "-l", "app=web", "--prefix=true", "-n", "default"]
        )

    def test_service_empty_selector_raises(self):
        cmd, _, kubectl = _make_command(kind=str(Kind.Service))
        kubectl.run.return_value = json.dumps({"spec": {}})
        with pytest.raises(ValueError, match="no pod selector"):
            cmd.execute(1)

    def test_service_null_selector_raises(self):
        cmd, _, kubectl = _make_command(kind=str(Kind.Service))
        kubectl.run.return_value = json.dumps({"spec": {"selector": None}})
        with pytest.raises(ValueError, match="no pod selector"):
            cmd.execute(1)


def test_aggregate_logs_wraps_selector_lookup_in_status():
    state = MagicMock()
    state.fields.return_value = ("web", "default", str(Kind.Deployment))
    kubectl = MagicMock()
    kubectl.run.return_value = _workload_json({"app": "web"})
    kubectl.run_interactive.return_value = 0
    entered = []

    class FakeStatus:
        def __init__(self, message):
            self.message = message

        def __enter__(self):
            entered.append(self.message)
            kubectl.run.assert_not_called()

        def __exit__(self, *args):
            kubectl.run.assert_called_once()

    LogsCommand(state=state, kubectl=kubectl, status=FakeStatus).execute(1)

    assert entered == ["resolving pod selector"]
    kubectl.run_interactive.assert_called_once()


class TestLogsStaleDetection:
    def _command(self, rc, probe_rc):
        cmd, _, kubectl = _make_command(name="web-1")
        kubectl.run_interactive.return_value = rc
        kubectl.probe.return_value = probe_rc
        return cmd, kubectl

    def test_nonzero_exit_with_missing_pod_raises_stale(self):
        cmd, kubectl = self._command(rc=1, probe_rc=1)
        with pytest.raises(StaleResourceError, match="Pod/web-1"):
            cmd.execute(1)
        kubectl.probe.assert_called_once_with(["get", "Pod", "web-1", "-n", "default"])

    def test_nonzero_exit_with_live_pod_stays_silent(self):
        cmd, _ = self._command(rc=1, probe_rc=0)
        cmd.execute(1)

    def test_zero_exit_skips_probe(self):
        cmd, kubectl = self._command(rc=0, probe_rc=1)
        cmd.execute(1)
        kubectl.probe.assert_not_called()
