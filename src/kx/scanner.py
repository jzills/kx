import json
import subprocess
from dataclasses import dataclass
from typing import Protocol

# Canonical severity buckets, most-severe first. Scout's SARIF uses exactly
# these labels in rule.properties.cvssV3_severity.
SEVERITIES = ("CRITICAL", "HIGH", "MEDIUM", "LOW", "UNSPECIFIED")


@dataclass
class ImageScan:
    """Rolled-up scan result for one image. counts is None when the scan
    itself failed (unpullable image, auth, unparseable output); error holds a
    short reason for the table's status cell."""

    image: str
    counts: dict[str, int] | None = None
    error: str | None = None


class ScannerServiceProtocol(Protocol):
    def scan(self, argv: list[str]) -> int: ...
    def capture(self, argv: list[str]) -> subprocess.CompletedProcess: ...
    def probe(self, argv: list[str]) -> int: ...


def _missing_binary(argv: list[str]) -> str:
    return f"{argv[0]} not found on PATH — install it to run this scan."


class ScannerService:
    def scan(self, argv: list[str]) -> int:
        # Inherit stdio so the scanner streams its own output straight to the
        # terminal (native passthrough); return the exit code without raising.
        try:
            return subprocess.run(argv).returncode
        except FileNotFoundError as e:
            raise RuntimeError(_missing_binary(argv)) from e

    def capture(self, argv: list[str]) -> subprocess.CompletedProcess:
        # Capture stdout for structured (SARIF) parsing into a summary table.
        try:
            return subprocess.run(argv, capture_output=True, text=True)
        except FileNotFoundError as e:
            raise RuntimeError(_missing_binary(argv)) from e

    def probe(self, argv: list[str]) -> int:
        # Silent availability check: swallow output, report only the exit code.
        try:
            return subprocess.run(argv, capture_output=True, text=True).returncode
        except FileNotFoundError as e:
            raise RuntimeError(_missing_binary(argv)) from e


class Engine:
    name: str

    def preflight_argv(self) -> list[str]:
        """Cheap command that exits 0 only when the scanner is usable."""
        raise NotImplementedError

    def unavailable_message(self) -> str:
        """Actionable message when preflight_argv() fails."""
        raise NotImplementedError

    def passthrough_argv(self, image: str, extra: list[str] | None = None) -> list[str]:
        raise NotImplementedError

    def summary_argv(self, image: str) -> list[str]:
        raise NotImplementedError

    def parse_counts(self, stdout: str) -> dict[str, int]:
        raise NotImplementedError


SCOUT_DOCS_URL = "https://docs.docker.com/scout/"


class ScoutEngine(Engine):
    name = "scout"

    def preflight_argv(self) -> list[str]:
        # `docker scout` is an optional CLI plugin; a plain Docker install
        # answers `docker scout version` with "unknown command" and exits 1.
        return ["docker", "scout", "version"]

    def unavailable_message(self) -> str:
        return (
            "docker scout is not available — kx scan needs the Docker Scout "
            f"CLI plugin. Install it: {SCOUT_DOCS_URL}"
        )

    def passthrough_argv(self, image: str, extra: list[str] | None = None) -> list[str]:
        return ["docker", "scout", "cves", image, *(extra or [])]

    def summary_argv(self, image: str) -> list[str]:
        return ["docker", "scout", "cves", "--format", "sarif", image]

    def parse_counts(self, stdout: str) -> dict[str, int]:
        data = json.loads(stdout)
        counts = {severity: 0 for severity in SEVERITIES}
        for run in data.get("runs", []):
            rules = run.get("tool", {}).get("driver", {}).get("rules", [])
            for result in run.get("results", []):
                index = result.get("ruleIndex")
                severity = "UNSPECIFIED"
                if isinstance(index, int) and 0 <= index < len(rules):
                    severity = (
                        rules[index].get("properties", {}).get("cvssV3_severity")
                        or "UNSPECIFIED"
                    ).upper()
                if severity not in counts:
                    severity = "UNSPECIFIED"
                counts[severity] += 1
        return counts


ENGINES: dict[str, Engine] = {
    "scout": ScoutEngine(),
}


def get_engine(name: str) -> Engine:
    engine = ENGINES.get(name)
    if engine is None:
        known = ", ".join(sorted(ENGINES))
        raise ValueError(f"unknown engine '{name}'. Available engines: {known}.")
    return engine
