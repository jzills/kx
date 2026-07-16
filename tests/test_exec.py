import subprocess
import pytest
from unittest.mock import MagicMock
from kx.commands.exec import ExecCommand
from kx.kinds import Kind
from kx.refresh import StaleResourceError


def _probe(shell_rc, get_rc):
    """Answer shell-detection probes (exec …) and existence probes (get …)
    separately."""

    def side_effect(args):
        return shell_rc if args[0] == "exec" else get_rc

    return side_effect


def _make_command(name="nginx", namespace="default", kind=Kind.Pod, shells=None):
    state = MagicMock()
    state.fields.return_value = (name, namespace, kind)
    kubectl = MagicMock()
    kubectl.run_interactive.return_value = 0
    kubectl.probe.return_value = 0
    kwargs = {"state": state, "kubectl": kubectl}
    if shells is not None:
        kwargs["shells"] = shells
    return ExecCommand(**kwargs), state, kubectl


class TestExecCommand:
    def test_default_shell_bash(self):
        cmd, _, kubectl = _make_command()
        kubectl.probe.return_value = 0
        cmd.execute(1, None)
        kubectl.probe.assert_called_once_with(
            ["exec", "nginx", "-n", "default", "--", "bash", "-c", "exit 0"]
        )
        kubectl.run_interactive.assert_called_once_with(
            ["exec", "-it", "nginx", "-n", "default", "--", "bash"]
        )

    def test_falls_back_to_sh_when_bash_probe_fails(self):
        cmd, _, kubectl = _make_command()
        kubectl.probe.side_effect = [1, 0]
        cmd.execute(1, None)
        assert kubectl.probe.call_count == 2
        kubectl.run_interactive.assert_called_once_with(
            ["exec", "-it", "nginx", "-n", "default", "--", "sh"]
        )

    def test_error_when_both_shells_fail(self):
        cmd, _, kubectl = _make_command()
        kubectl.probe.side_effect = _probe(shell_rc=1, get_rc=0)
        with pytest.raises(ValueError, match="No shell found"):
            cmd.execute(1, None)
        # Two shell probes plus the existence check.
        assert kubectl.probe.call_count == 3
        kubectl.run_interactive.assert_not_called()

    def test_explicit_cmd(self):
        cmd, _, kubectl = _make_command()
        cmd.execute(1, ["python3"])
        kubectl.run_interactive.assert_called_once_with(
            ["exec", "-it", "nginx", "-n", "default", "--", "python3"],
            stderr=subprocess.DEVNULL,
        )

    def test_explicit_cmd_failure_raises_value_error(self):
        cmd, _, kubectl = _make_command()
        kubectl.run_interactive.return_value = 1
        with pytest.raises(ValueError, match="Command failed in container"):
            cmd.execute(1, ["env"])

    def test_non_pod_raises_value_error(self):
        cmd, _, _ = _make_command(kind=Kind.Deployment)
        with pytest.raises(ValueError, match="exec is only supported for pods"):
            cmd.execute(1, None)

    def test_uses_state_fields(self):
        cmd, state, _ = _make_command(name="my-pod", namespace="kube-system")
        cmd.execute(3, None)
        state.fields.assert_called_once_with(3)

    def test_extra_args_with_default_shell(self):
        cmd, _, kubectl = _make_command()
        cmd.execute(1, None, extra_args=["-c", "sidecar"])
        kubectl.probe.assert_called_once_with(
            [
                "exec",
                "nginx",
                "-n",
                "default",
                "-c",
                "sidecar",
                "--",
                "bash",
                "-c",
                "exit 0",
            ]
        )
        kubectl.run_interactive.assert_called_once_with(
            ["exec", "-it", "nginx", "-n", "default", "-c", "sidecar", "--", "bash"]
        )

    def test_extra_args_with_explicit_cmd(self):
        cmd, _, kubectl = _make_command()
        cmd.execute(1, ["sh"], extra_args=["-c", "sidecar"])
        kubectl.run_interactive.assert_called_once_with(
            ["exec", "-it", "nginx", "-n", "default", "-c", "sidecar", "--", "sh"],
            stderr=subprocess.DEVNULL,
        )

    def test_custom_shells_used_in_order(self):
        cmd, _, kubectl = _make_command(shells=("zsh", "bash"))
        kubectl.probe.side_effect = [1, 0]
        cmd.execute(1, None)
        assert kubectl.probe.call_count == 2
        kubectl.run_interactive.assert_called_once_with(
            ["exec", "-it", "nginx", "-n", "default", "--", "bash"]
        )


class TestExecStaleDetection:
    def test_explicit_cmd_failure_on_missing_pod_raises_stale(self):
        cmd, _, kubectl = _make_command(name="web-1")
        kubectl.run_interactive.return_value = 1
        kubectl.probe.return_value = 1
        with pytest.raises(StaleResourceError, match="Pod/web-1"):
            cmd.execute(1, ["env"])

    def test_explicit_cmd_failure_on_live_pod_raises_value_error(self):
        cmd, _, kubectl = _make_command(name="web-1")
        kubectl.run_interactive.return_value = 1
        kubectl.probe.return_value = 0
        with pytest.raises(ValueError, match="Command failed in container"):
            cmd.execute(1, ["env"])

    def test_no_shell_on_missing_pod_raises_stale(self):
        cmd, _, kubectl = _make_command(name="web-1")
        kubectl.probe.side_effect = _probe(shell_rc=1, get_rc=1)
        with pytest.raises(StaleResourceError, match="Pod/web-1"):
            cmd.execute(1, None)
