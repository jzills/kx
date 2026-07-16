import pytest
from unittest.mock import MagicMock
from kx.commands.describe import DescribeCommand
from kx.kinds import Kind
from kx.refresh import StaleResourceError


def _make_command(name="nginx", namespace="default", kind=str(Kind.Pod)):
    state = MagicMock()
    state.fields.return_value = (name, namespace, kind)
    kubectl = MagicMock()
    kubectl.run_interactive.return_value = 0
    return DescribeCommand(state=state, kubectl=kubectl), state, kubectl


class TestDescribeCommand:
    def test_basic_describe(self):
        cmd, _, kubectl = _make_command()
        cmd.execute(1)
        kubectl.run_interactive.assert_called_once_with(
            ["describe", "Pod", "nginx", "-n", "default"]
        )

    def test_uses_state_fields(self):
        cmd, state, _ = _make_command(name="my-pod", namespace="kube-system")
        cmd.execute(3)
        state.fields.assert_called_once_with(3)

    def test_extra_args_passed_through(self):
        cmd, _, kubectl = _make_command()
        cmd.execute(1, ["--show-events=false"])
        kubectl.run_interactive.assert_called_once_with(
            ["describe", "Pod", "nginx", "-n", "default", "--show-events=false"]
        )

    def test_multiple_extra_args(self):
        cmd, _, kubectl = _make_command()
        cmd.execute(1, ["--show-events=false", "--chunk-size=500"])
        kubectl.run_interactive.assert_called_once_with(
            [
                "describe",
                "Pod",
                "nginx",
                "-n",
                "default",
                "--show-events=false",
                "--chunk-size=500",
            ]
        )


class TestDescribeStaleDetection:
    def _command(self, rc, probe_rc):
        cmd, _, kubectl = _make_command(name="web-1")
        kubectl.run_interactive.return_value = rc
        kubectl.probe.return_value = probe_rc
        return cmd, kubectl

    def test_nonzero_exit_with_missing_resource_raises_stale(self):
        cmd, kubectl = self._command(rc=1, probe_rc=1)
        with pytest.raises(StaleResourceError, match="Pod/web-1"):
            cmd.execute(1)
        kubectl.probe.assert_called_once_with(["get", "Pod", "web-1", "-n", "default"])

    def test_nonzero_exit_with_live_resource_stays_silent(self):
        cmd, _ = self._command(rc=1, probe_rc=0)
        cmd.execute(1)

    def test_zero_exit_skips_probe(self):
        cmd, kubectl = self._command(rc=0, probe_rc=1)
        cmd.execute(1)
        kubectl.probe.assert_not_called()
