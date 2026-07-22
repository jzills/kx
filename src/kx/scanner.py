import subprocess
from collections.abc import Callable
from typing import Protocol


class ScannerServiceProtocol(Protocol):
    def scan(self, engine_argv: list[str]) -> int: ...


class ScannerService:
    def scan(self, engine_argv: list[str]) -> int:
        # Inherit stdio so the scanner streams its own output straight to the
        # terminal (native passthrough); return the exit code without raising.
        return subprocess.run(engine_argv).returncode


def _scout_argv(image: str) -> list[str]:
    return ["docker", "scout", "cves", image]


# name -> argv builder. Add trivy/grype here when needed.
ENGINES: dict[str, Callable[[str], list[str]]] = {
    "scout": _scout_argv,
}


def build_engine_argv(
    engine: str, image: str, extra: list[str] | None = None
) -> list[str]:
    builder = ENGINES.get(engine)
    if builder is None:
        known = ", ".join(sorted(ENGINES))
        raise ValueError(f"unknown engine '{engine}'. Available engines: {known}.")
    return [*builder(image), *(extra or [])]
