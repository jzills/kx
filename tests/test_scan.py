import json
from unittest.mock import MagicMock, patch

import pytest
from typer.testing import CliRunner

from kx.commands.scan import ScanCommand
from kx.kinds import Kind
from kx.main import app


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
            "spec": {
                "containers": [
                    {"name": f"c{i}", "image": img} for i, img in enumerate(images)
                ]
            }
        }
    )


def _make_command(name="web", namespace="default", kind=Kind.Deployment, get_json=None):
    state = MagicMock()
    state.fields.return_value = (name, namespace, kind)
    kubectl = MagicMock()
    kubectl.run.return_value = (
        get_json if get_json is not None else _deployment_json(["nginx:1.25"])
    )
    scanner = MagicMock()
    scanner.scan.return_value = 0
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

    def test_no_images_raises(self):
        cmd, _, _, _ = _make_command(get_json=_deployment_json([]))
        with pytest.raises(
            ValueError, match="no container images found for Deployment/web"
        ):
            cmd.execute(1)


class TestScanCli:
    def test_scan_prints_banner_and_scans_each_image(self):
        state = MagicMock()
        state.fields.return_value = ("web", "default", Kind.Deployment)
        kubectl = MagicMock()
        kubectl.run.return_value = _deployment_json(["nginx:1.25", "redis:7"])
        scanner = MagicMock()
        scanner.scan.return_value = 0
        with (
            patch("kx.main._state", state),
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._scanner", scanner),
        ):
            result = CliRunner().invoke(app, ["scan", "1"])
        assert result.exit_code == 0
        assert "Deployment/web" in result.output
        assert "2 images" in result.output
        assert "nginx:1.25" in result.output
        assert "redis:7" in result.output
        assert scanner.scan.call_count == 2

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
