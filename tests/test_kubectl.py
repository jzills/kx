import subprocess
from unittest.mock import patch

import pytest

from kx.kubectl import KubectlService


def _run_returning(returncode: int, stdout: str = "", stderr: str = ""):
    """A subprocess.run stand-in that faithfully honors check=True (raising
    CalledProcessError on a non-zero exit), so tests exercise the same
    contract the real subprocess enforces."""

    def fake(args, check=False, **kwargs):
        if check and returncode != 0:
            raise subprocess.CalledProcessError(returncode, args, stderr=stderr)
        return subprocess.CompletedProcess(args, returncode, stdout, stderr)

    return fake


class TestKubectlMissingBinary:
    def test_run_translates_missing_kubectl(self):
        with patch("kx.kubectl.subprocess.run", side_effect=FileNotFoundError()):
            with pytest.raises(RuntimeError, match="kubectl not found"):
                KubectlService().run(["get", "pods"])

    def test_run_interactive_translates_missing_kubectl(self):
        with patch("kx.kubectl.subprocess.run", side_effect=FileNotFoundError()):
            with pytest.raises(RuntimeError, match="kubectl not found"):
                KubectlService().run_interactive(["describe", "pod", "x"])

    def test_probe_translates_missing_kubectl(self):
        with patch("kx.kubectl.subprocess.run", side_effect=FileNotFoundError()):
            with pytest.raises(RuntimeError, match="kubectl not found"):
                KubectlService().probe(["get", "pod", "x"])

    def test_current_namespace_translates_missing_kubectl(self):
        with patch("kx.kubectl.subprocess.run", side_effect=FileNotFoundError()):
            with pytest.raises(RuntimeError, match="kubectl not found"):
                KubectlService().current_namespace()

    def test_current_context_translates_missing_kubectl(self):
        with patch("kx.kubectl.subprocess.run", side_effect=FileNotFoundError()):
            with pytest.raises(RuntimeError, match="kubectl not found"):
                KubectlService().current_context()


class TestKubectlRun:
    def test_returns_stdout_on_success(self):
        with patch("kx.kubectl.subprocess.run", _run_returning(0, stdout="ok\n")):
            assert KubectlService().run(["get", "pods"]) == "ok\n"

    def test_raises_runtime_error_with_stderr_on_nonzero(self):
        with patch("kx.kubectl.subprocess.run", _run_returning(1, stderr="  boom  \n")):
            with pytest.raises(RuntimeError, match="boom"):
                KubectlService().run(["get", "pods"])


class TestCurrentNamespace:
    def test_returns_configured_namespace(self):
        with patch("kx.kubectl.subprocess.run", _run_returning(0, stdout="prod\n")):
            assert KubectlService().current_namespace() == "prod"

    def test_empty_namespace_falls_back_to_default(self):
        with patch("kx.kubectl.subprocess.run", _run_returning(0, stdout="\n")):
            assert KubectlService().current_namespace() == "default"

    def test_nonzero_exit_falls_back_to_default(self):
        # No kubeconfig / no current context: a best-effort default, not a crash.
        with patch("kx.kubectl.subprocess.run", _run_returning(1, stderr="error")):
            assert KubectlService().current_namespace() == "default"


class TestCurrentContext:
    def test_returns_current_context(self):
        with patch("kx.kubectl.subprocess.run", _run_returning(0, stdout="minikube\n")):
            assert KubectlService().current_context() == "minikube"

    def test_nonzero_exit_returns_empty(self):
        # `kubectl config current-context` exits non-zero when none is set;
        # listing contexts must still work, so this is best-effort empty.
        with patch(
            "kx.kubectl.subprocess.run",
            _run_returning(1, stderr="error: current-context is not set"),
        ):
            assert KubectlService().current_context() == ""
