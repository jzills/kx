import json
from unittest.mock import patch

import pytest

from kx.scanner import SEVERITIES, ScannerService, ScoutEngine, get_engine


class TestScannerMissingBinary:
    def test_capture_translates_missing_binary(self):
        with patch("kx.scanner.subprocess.run", side_effect=FileNotFoundError()):
            with pytest.raises(RuntimeError, match="docker not found"):
                ScannerService().capture(["docker", "scout", "cves", "nginx"])

    def test_scan_translates_missing_binary(self):
        with patch("kx.scanner.subprocess.run", side_effect=FileNotFoundError()):
            with pytest.raises(RuntimeError, match="docker not found"):
                ScannerService().scan(["docker", "scout", "cves", "nginx"])


def _sarif(**sev_counts):
    """Build a minimal scout-shaped SARIF doc with the given per-severity
    finding counts."""
    rules = []
    results = []
    for severity, n in sev_counts.items():
        for _ in range(n):
            results.append({"ruleIndex": len(rules)})
            rules.append({"properties": {"cvssV3_severity": severity}})
    return json.dumps(
        {"runs": [{"tool": {"driver": {"rules": rules}}, "results": results}]}
    )


class TestGetEngine:
    def test_returns_scout(self):
        assert isinstance(get_engine("scout"), ScoutEngine)

    def test_unknown_engine_raises(self):
        with pytest.raises(ValueError, match="unknown engine 'bogus'"):
            get_engine("bogus")

    def test_unknown_engine_lists_available(self):
        with pytest.raises(ValueError, match="scout"):
            get_engine("bogus")


class TestScoutEngine:
    def test_passthrough_argv(self):
        assert ScoutEngine().passthrough_argv("nginx:1.25") == [
            "docker",
            "scout",
            "cves",
            "nginx:1.25",
        ]

    def test_passthrough_appends_extra_args(self):
        assert ScoutEngine().passthrough_argv("nginx:1.25", ["--only-fixed"]) == [
            "docker",
            "scout",
            "cves",
            "nginx:1.25",
            "--only-fixed",
        ]

    def test_summary_argv_uses_sarif(self):
        assert ScoutEngine().summary_argv("nginx:1.25") == [
            "docker",
            "scout",
            "cves",
            "--format",
            "sarif",
            "nginx:1.25",
        ]

    def test_parse_counts_tallies_by_severity(self):
        sarif = _sarif(CRITICAL=2, HIGH=3, MEDIUM=1, LOW=4, UNSPECIFIED=5)
        counts = ScoutEngine().parse_counts(sarif)
        assert counts == {
            "CRITICAL": 2,
            "HIGH": 3,
            "MEDIUM": 1,
            "LOW": 4,
            "UNSPECIFIED": 5,
        }

    def test_parse_counts_all_severities_present(self):
        counts = ScoutEngine().parse_counts(_sarif(HIGH=1))
        assert set(counts) == set(SEVERITIES)
        assert counts["HIGH"] == 1
        assert counts["CRITICAL"] == 0

    def test_parse_counts_empty_report(self):
        empty = json.dumps(
            {"runs": [{"tool": {"driver": {"rules": []}}, "results": []}]}
        )
        assert ScoutEngine().parse_counts(empty) == {s: 0 for s in SEVERITIES}

    def test_parse_counts_unknown_severity_folds_to_unspecified(self):
        sarif = json.dumps(
            {
                "runs": [
                    {
                        "tool": {
                            "driver": {
                                "rules": [{"properties": {"cvssV3_severity": "WEIRD"}}]
                            }
                        },
                        "results": [{"ruleIndex": 0}],
                    }
                ]
            }
        )
        assert ScoutEngine().parse_counts(sarif)["UNSPECIFIED"] == 1
