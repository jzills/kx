import json
import subprocess
from unittest.mock import MagicMock, patch

import pytest
from typer.testing import CliRunner

from kx.commands.scan import ScanCommand
from kx.kinds import Kind
from kx.main import app


def _sarif(**sev_counts):
    rules = []
    results = []
    for severity, n in sev_counts.items():
        for _ in range(n):
            results.append({"ruleIndex": len(rules)})
            rules.append({"properties": {"cvssV3_severity": severity}})
    return json.dumps(
        {"runs": [{"tool": {"driver": {"rules": rules}}, "results": results}]}
    )


def _captured(stdout="", returncode=0, stderr=""):
    return subprocess.CompletedProcess(
        args=[], returncode=returncode, stdout=stdout, stderr=stderr
    )


def _deployment_json(images, init_images=None):
    containers = [{"name": f"c{i}", "image": img} for i, img in enumerate(images)]
    spec = {"containers": containers}
    if init_images is not None:
        spec["initContainers"] = [
            {"name": f"i{i}", "image": img} for i, img in enumerate(init_images)
        ]
    return json.dumps({"spec": {"template": {"spec": spec}}})


def _pod_json(images):
    return json.dumps(
        {
            "kind": "Pod",
            "spec": {
                "containers": [
                    {"name": f"c{i}", "image": img} for i, img in enumerate(images)
                ]
            },
        }
    )


def _pod_spec(images):
    return {
        "containers": [{"name": f"c{i}", "image": img} for i, img in enumerate(images)]
    }


def _deployment_item(images):
    return {"kind": "Deployment", "spec": {"template": {"spec": _pod_spec(images)}}}


def _cronjob_item(images):
    return {
        "kind": "CronJob",
        "spec": {"jobTemplate": {"spec": {"template": {"spec": _pod_spec(images)}}}},
    }


def _pod_item(images):
    return {"kind": "Pod", "spec": _pod_spec(images)}


def _list_json(*items):
    return json.dumps({"kind": "List", "items": list(items)})


def _scanner_mock(probe=0):
    """A scanner whose preflight passes by default; probe=1 simulates a Docker
    install without the Scout plugin."""
    scanner = MagicMock()
    scanner.probe.return_value = probe
    scanner.scan.return_value = 0
    return scanner


def _make_command(name="web", namespace="default", kind=Kind.Deployment, get_json=None):
    state = MagicMock()
    state.fields.return_value = (name, namespace, kind)
    kubectl = MagicMock()
    kubectl.run.return_value = (
        get_json if get_json is not None else _deployment_json(["nginx:1.25"])
    )
    scanner = _scanner_mock()
    return (
        ScanCommand(state=state, kubectl=kubectl, scanner=scanner),
        state,
        kubectl,
        scanner,
    )


class TestScanCommand:
    def test_extracts_images_from_deployment(self):
        cmd, _, kubectl, _ = _make_command(
            get_json=_deployment_json(["nginx:1.25", "redis:7"])
        )
        images = cmd.execute(1)
        kubectl.run.assert_called_once_with(
            ["get", "Deployment", "web", "-n", "default", "-o", "json"]
        )
        assert images == ["nginx:1.25", "redis:7"]

    def test_includes_init_containers_first(self):
        cmd, _, _, _ = _make_command(
            get_json=_deployment_json(["nginx:1.25"], init_images=["busybox:1"])
        )
        assert cmd.execute(1) == ["busybox:1", "nginx:1.25"]

    def test_dedupes_preserving_order(self):
        cmd, _, _, _ = _make_command(
            get_json=_deployment_json(["nginx:1.25", "redis:7", "nginx:1.25"])
        )
        assert cmd.execute(1) == ["nginx:1.25", "redis:7"]

    def test_bare_pod_reads_spec_containers(self):
        cmd, _, _, _ = _make_command(kind=Kind.Pod, get_json=_pod_json(["nginx:1.25"]))
        assert cmd.execute(1) == ["nginx:1.25"]

    def test_cronjob_reads_nested_job_template(self):
        cmd, _, _, _ = _make_command(
            kind=Kind.CronJob, get_json=json.dumps(_cronjob_item(["busybox:1"]))
        )
        assert cmd.execute(1) == ["busybox:1"]

    def test_uses_state_fields(self):
        cmd, state, _, _ = _make_command()
        cmd.execute(3)
        state.fields.assert_called_once_with(3)

    def test_scan_image_builds_scout_argv(self):
        cmd, _, _, scanner = _make_command()
        rc = cmd.scan_image("scout", "nginx:1.25")
        scanner.scan.assert_called_once_with(["docker", "scout", "cves", "nginx:1.25"])
        assert rc == 0

    def test_scan_image_passes_extra_args(self):
        cmd, _, _, scanner = _make_command()
        cmd.scan_image("scout", "nginx:1.25", ["--only-fixed"])
        scanner.scan.assert_called_once_with(
            ["docker", "scout", "cves", "nginx:1.25", "--only-fixed"]
        )

    def test_unsupported_kind_raises(self):
        cmd, _, _, _ = _make_command(kind=Kind.ConfigMap)
        with pytest.raises(ValueError, match="scan is not supported for 'ConfigMap'"):
            cmd.execute(1)

    def test_unknown_engine_raises_before_cluster_call(self):
        cmd, _, kubectl, _ = _make_command()
        with pytest.raises(ValueError, match="unknown engine 'bogus'"):
            cmd.execute(1, engine="bogus")
        kubectl.run.assert_not_called()

    def test_preflights_engine_before_cluster_call(self):
        cmd, _, kubectl, scanner = _make_command()
        cmd.execute(1)
        scanner.probe.assert_called_once_with(["docker", "scout", "version"])

    def test_missing_scout_plugin_raises_before_cluster_call(self):
        cmd, _, kubectl, scanner = _make_command()
        scanner.probe.return_value = 1
        with pytest.raises(RuntimeError, match="https://docs.docker.com/scout/"):
            cmd.execute(1)
        kubectl.run.assert_not_called()

    def test_no_images_raises(self):
        cmd, _, _, _ = _make_command(get_json=_deployment_json([]))
        with pytest.raises(
            ValueError, match="no container images found for Deployment/web"
        ):
            cmd.execute(1)


class TestScanCollectNamespace:
    def _cmd(self, list_json):
        state = MagicMock()
        kubectl = MagicMock()
        kubectl.run.return_value = list_json
        scanner = _scanner_mock()
        return ScanCommand(state=state, kubectl=kubectl, scanner=scanner), kubectl

    def test_lists_all_workload_kinds(self):
        cmd, kubectl = self._cmd(_list_json(_deployment_item(["nginx:1.25"])))
        cmd.collect_namespace("prod")
        kubectl.run.assert_called_once_with(
            [
                "get",
                "deployments,statefulsets,daemonsets,cronjobs,jobs,pods",
                "-n",
                "prod",
                "-o",
                "json",
            ]
        )

    def test_unions_and_dedupes_across_kinds(self):
        cmd, _ = self._cmd(
            _list_json(
                _deployment_item(["nginx:1.27", "redis:7"]),
                _cronjob_item(["busybox:1"]),
                _pod_item(["nginx:1.27"]),
            )
        )
        assert cmd.collect_namespace("default") == [
            "nginx:1.27",
            "redis:7",
            "busybox:1",
        ]

    def test_empty_namespace_raises(self):
        cmd, _ = self._cmd(_list_json())
        with pytest.raises(
            ValueError, match="no container images found in namespace 'default'"
        ):
            cmd.collect_namespace("default")

    def test_unknown_engine_raises_before_cluster_call(self):
        cmd, kubectl = self._cmd(_list_json(_deployment_item(["nginx:1.25"])))
        with pytest.raises(ValueError, match="unknown engine 'bogus'"):
            cmd.collect_namespace("default", engine="bogus")
        kubectl.run.assert_not_called()

    def test_missing_scout_plugin_raises_before_cluster_call(self):
        cmd, kubectl = self._cmd(_list_json(_deployment_item(["nginx:1.25"])))
        cmd.scanner.probe.return_value = 1
        with pytest.raises(RuntimeError, match="https://docs.docker.com/scout/"):
            cmd.collect_namespace("default")
        kubectl.run.assert_not_called()


class TestScanSummarize:
    def _cmd(self, capture_results):
        scanner = MagicMock()
        scanner.capture.side_effect = capture_results
        return (
            ScanCommand(state=MagicMock(), kubectl=MagicMock(), scanner=scanner),
            scanner,
        )

    def test_uses_sarif_summary_argv(self):
        cmd, scanner = self._cmd([_captured(_sarif(HIGH=1))])
        cmd.summarize("scout", ["nginx:1.25"])
        scanner.capture.assert_called_once_with(
            ["docker", "scout", "cves", "--format", "sarif", "nginx:1.25"]
        )

    def test_returns_counts_per_image(self):
        cmd, _ = self._cmd(
            [_captured(_sarif(CRITICAL=1, HIGH=2)), _captured(_sarif(LOW=3))]
        )
        rows = cmd.summarize("scout", ["nginx:1.25", "redis:7"])
        assert rows[0].image == "nginx:1.25"
        assert rows[0].counts["CRITICAL"] == 1
        assert rows[0].counts["HIGH"] == 2
        assert rows[1].counts["LOW"] == 3

    def test_failed_scan_becomes_error_row(self):
        cmd, _ = self._cmd(
            [_captured(returncode=1, stderr="no such image: api/bad:latest")]
        )
        rows = cmd.summarize("scout", ["api/bad:latest"])
        assert rows[0].counts is None
        assert "no such image" in rows[0].error

    def test_unparseable_output_becomes_error_row(self):
        cmd, _ = self._cmd([_captured(stdout="not json")])
        rows = cmd.summarize("scout", ["nginx:1.25"])
        assert rows[0].counts is None
        assert rows[0].error == "unparseable output"

    def test_unknown_engine_raises(self):
        cmd, _ = self._cmd([])
        with pytest.raises(ValueError, match="unknown engine 'bogus'"):
            cmd.summarize("bogus", ["nginx:1.25"])


class TestScanCli:
    def test_scan_prints_summary_table_by_default(self):
        state = MagicMock()
        state.fields.return_value = ("web", "default", Kind.Deployment)
        kubectl = MagicMock()
        kubectl.run.return_value = _deployment_json(["nginx:1.25", "redis:7"])
        scanner = _scanner_mock()
        scanner.capture.side_effect = [
            _captured(_sarif(CRITICAL=1, HIGH=2)),
            _captured(_sarif(LOW=3)),
        ]
        with (
            patch("kx.main._state", state),
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._scanner", scanner),
        ):
            result = CliRunner().invoke(app, ["scan", "1"])
        assert result.exit_code == 0
        assert "Deployment/web" in result.output
        assert "2 images" in result.output
        # Table headers and per-image rows, no raw scanner streaming.
        assert "CRIT" in result.output
        assert "UNSPEC" in result.output
        assert "nginx:1.25" in result.output
        assert "redis:7" in result.output
        assert scanner.capture.call_count == 2
        scanner.scan.assert_not_called()

    def test_scan_full_streams_raw_output(self):
        state = MagicMock()
        state.fields.return_value = ("web", "default", Kind.Deployment)
        kubectl = MagicMock()
        kubectl.run.return_value = _deployment_json(["nginx:1.25", "redis:7"])
        scanner = _scanner_mock()
        with (
            patch("kx.main._state", state),
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._scanner", scanner),
        ):
            result = CliRunner().invoke(app, ["scan", "1", "--full"])
        assert result.exit_code == 0
        assert "nginx:1.25" in result.output
        assert "redis:7" in result.output
        assert scanner.scan.call_count == 2
        scanner.capture.assert_not_called()

    def test_scan_summary_shows_error_row(self):
        state = MagicMock()
        state.fields.return_value = ("web", "default", Kind.Deployment)
        kubectl = MagicMock()
        kubectl.run.return_value = _deployment_json(["api/bad:latest"])
        scanner = _scanner_mock()
        scanner.capture.return_value = _captured(returncode=1, stderr="no such image")
        with (
            patch("kx.main._state", state),
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._scanner", scanner),
        ):
            result = CliRunner().invoke(app, ["scan", "1"])
        assert result.exit_code == 0
        assert "no such image" in result.output

    def test_scan_unknown_engine_exits_1(self):
        state = MagicMock()
        state.fields.return_value = ("web", "default", Kind.Deployment)
        with (
            patch("kx.main._state", state),
            patch("kx.main._kubectl", MagicMock()),
            patch("kx.main._scanner", MagicMock()),
        ):
            result = CliRunner().invoke(app, ["scan", "1", "--engine", "bogus"])
        assert result.exit_code == 1
        assert "unknown engine" in result.output

    def test_scan_missing_scout_plugin_exits_1_once(self):
        state = MagicMock()
        state.fields.return_value = ("web", "default", Kind.Deployment)
        kubectl = MagicMock()
        scanner = _scanner_mock(probe=1)
        with (
            patch("kx.main._state", state),
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._scanner", scanner),
        ):
            result = CliRunner().invoke(app, ["scan", "1"])
        assert result.exit_code == 1
        assert "docker scout is not available" in result.output
        assert "https://docs.docker.com/scout/" in result.output
        scanner.capture.assert_not_called()

    def test_scan_no_index_sweeps_namespace(self):
        kubectl = MagicMock()
        kubectl.current_namespace.return_value = "prod"
        kubectl.run.return_value = _list_json(
            _deployment_item(["nginx:1.27"]), _cronjob_item(["busybox:1"])
        )
        scanner = _scanner_mock()
        scanner.capture.side_effect = [
            _captured(_sarif(HIGH=1)),
            _captured(_sarif(MEDIUM=2)),
        ]
        with (
            patch("kx.main._state", MagicMock()),
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._scanner", scanner),
        ):
            result = CliRunner().invoke(app, ["scan"])
        assert result.exit_code == 0
        assert "Mixed · prod · 2 images" in result.output
        assert "nginx:1.27" in result.output
        assert "busybox:1" in result.output
        assert scanner.capture.call_count == 2
