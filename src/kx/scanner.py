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


class Engine:
    name: str

    def passthrough_argv(self, image: str, extra: list[str] | None = None) -> list[str]:
        raise NotImplementedError

    def summary_argv(self, image: str) -> list[str]:
        raise NotImplementedError

    def parse_counts(self, stdout: str) -> dict[str, int]:
        raise NotImplementedError


class ScoutEngine(Engine):
    name = "scout"

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
