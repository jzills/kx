import pytest
from unittest.mock import MagicMock
from kx.commands.port_forward import PortForwardCommand
from kx.kinds import Kind
from kx.refresh import StaleResourceError


def _make_command(name="nginx", namespace="default", kind=Kind.Pod):
    state = MagicMock()
    state.fields.return_value = (name, namespace, kind)
    kubectl = MagicMock()
    kubectl.run_interactive.return_value = 0
    return PortForwardCommand(kubectl=kubectl, state=state), state, kubectl


class TestPortForwardCommand:
    def test_basic_port_forward(self):
        cmd, _, kubectl = _make_command()
        cmd.execute(1, "8080:8080")
        kubectl.run_interactive.assert_called_once_with(
            ["port-forward", "Pod/nginx", "8080:8080", "-n", "default"]
        )

    def test_uses_state_fields(self):
        cmd, state, _ = _make_command(name="my-pod", namespace="kube-system")
        cmd.execute(3, "9090:9090")
        state.fields.assert_called_once_with(3)

    def test_unsupported_kind_raises_value_error(self):
        cmd, _, _ = _make_command(kind=Kind.ConfigMap)
        with pytest.raises(ValueError, match="port-forward is not supported"):
            cmd.execute(1, "8080:8080")

    def test_extra_args_passed_through(self):
        cmd, _, kubectl = _make_command()
        cmd.execute(1, "8080:8080", extra_args=["--address", "0.0.0.0"])
        kubectl.run_interactive.assert_called_once_with(
            [
                "port-forward",
                "Pod/nginx",
                "8080:8080",
                "-n",
                "default",
                "--address",
                "0.0.0.0",
            ]
        )

    def test_multiple_extra_args(self):
        cmd, _, kubectl = _make_command()
        cmd.execute(
            1,
            "8080:8080",
            extra_args=["--address", "0.0.0.0", "--pod-running-timeout=1m"],
        )
        kubectl.run_interactive.assert_called_once_with(
            [
                "port-forward",
                "Pod/nginx",
                "8080:8080",
                "-n",
                "default",
                "--address",
                "0.0.0.0",
                "--pod-running-timeout=1m",
            ]
        )


class TestPortForwardStaleDetection:
    def _command(self, rc, probe_rc):
        cmd, _, kubectl = _make_command(name="web-1")
        kubectl.run_interactive.return_value = rc
        kubectl.probe.return_value = probe_rc
        return cmd, kubectl

    def test_nonzero_exit_with_missing_resource_raises_stale(self):
        cmd, kubectl = self._command(rc=1, probe_rc=1)
        with pytest.raises(StaleResourceError, match="Pod/web-1"):
            cmd.execute(1, "8080:80")
        kubectl.probe.assert_called_once_with(["get", "Pod", "web-1", "-n", "default"])

    def test_nonzero_exit_with_live_resource_stays_silent(self):
        # Ctrl-C ends port-forward with a non-zero code; a live target stays quiet.
        cmd, _ = self._command(rc=130, probe_rc=0)
        cmd.execute(1, "8080:80")

    def test_zero_exit_skips_probe(self):
        cmd, kubectl = self._command(rc=0, probe_rc=1)
        cmd.execute(1, "8080:80")
        kubectl.probe.assert_not_called()
