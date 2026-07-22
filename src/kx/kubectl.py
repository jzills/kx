import subprocess
from typing import Protocol


class KubectlServiceProtocol(Protocol):
    def run(self, args: list[str]) -> str: ...
    def run_interactive(self, args: list[str], stderr: int | None = None) -> int: ...
    def probe(self, args: list[str]) -> int: ...
    def current_namespace(self) -> str: ...
    def current_context(self) -> str: ...


class KubectlService:
    def run(self, args: list[str]) -> str:
        result = subprocess.run(
            ["kubectl", *args],
            capture_output=True,
            text=True,
        )
        if result.returncode != 0:
            raise RuntimeError(result.stderr.strip())
        return result.stdout

    def run_interactive(self, args: list[str], stderr: int | None = None) -> int:
        result = subprocess.run(["kubectl", *args], stderr=stderr)
        return result.returncode

    def probe(self, args: list[str]) -> int:
        result = subprocess.run(["kubectl", *args], capture_output=True)
        return result.returncode

    def current_namespace(self) -> str:
        result = subprocess.run(
            ["kubectl", "config", "view", "--minify", "-o", "jsonpath={..namespace}"],
            capture_output=True,
            text=True,
            check=True,
        )
        ns = result.stdout.strip()
        return ns if ns else "default"

    def current_context(self) -> str:
        result = subprocess.run(
            ["kubectl", "config", "current-context"],
            capture_output=True,
            text=True,
            check=True,
        )
        return result.stdout.strip()
