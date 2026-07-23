import subprocess
from typing import Protocol

_MISSING_KUBECTL = (
    "kubectl not found on PATH — install kubectl "
    "(https://kubernetes.io/docs/tasks/tools/) and ensure it is on your PATH."
)


class KubectlServiceProtocol(Protocol):
    def run(self, args: list[str]) -> str: ...
    def run_interactive(self, args: list[str], stderr: int | None = None) -> int: ...
    def probe(self, args: list[str]) -> int: ...
    def current_namespace(self) -> str: ...
    def current_context(self) -> str: ...


class KubectlService:
    def _run(self, args: list[str], **kwargs) -> subprocess.CompletedProcess:
        # Central chokepoint: a missing kubectl raises FileNotFoundError, which
        # handle_errors doesn't catch (traceback). Translate it into a handled
        # RuntimeError with an actionable message.
        try:
            return subprocess.run(["kubectl", *args], **kwargs)
        except FileNotFoundError as e:
            raise RuntimeError(_MISSING_KUBECTL) from e

    def run(self, args: list[str]) -> str:
        result = self._run(args, capture_output=True, text=True)
        if result.returncode != 0:
            raise RuntimeError(result.stderr.strip())
        return result.stdout

    def run_interactive(self, args: list[str], stderr: int | None = None) -> int:
        return self._run(args, stderr=stderr).returncode

    def probe(self, args: list[str]) -> int:
        return self._run(args, capture_output=True).returncode

    def current_namespace(self) -> str:
        # Best-effort: no kubeconfig / no current context exits non-zero, and
        # `check=True` would surface that as an unhandled CalledProcessError.
        # The namespace is only a label here, so fall back to "default".
        result = self._run(
            ["config", "view", "--minify", "-o", "jsonpath={..namespace}"],
            capture_output=True,
            text=True,
        )
        if result.returncode != 0:
            return "default"
        ns = result.stdout.strip()
        return ns if ns else "default"

    def current_context(self) -> str:
        # `kubectl config current-context` exits non-zero when none is set;
        # return empty so context listing still works instead of crashing.
        result = self._run(
            ["config", "current-context"],
            capture_output=True,
            text=True,
        )
        if result.returncode != 0:
            return ""
        return result.stdout.strip()
