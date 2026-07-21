from unittest.mock import MagicMock, patch

from typer.testing import CliRunner

from kx.main import app

runner = CliRunner()


def _make_mocks(kubectl_output="NAME  CPU  MEMORY\nnginx  1m  10Mi"):
    kubectl = MagicMock()
    kubectl.run.return_value = kubectl_output
    kubectl.current_namespace.return_value = "default"
    state = MagicMock()
    state.load.return_value = MagicMock(namespace="default")
    index = MagicMock()
    index.add.return_value = ("1  nginx  1m  10Mi", ["nginx"])
    index.filter.side_effect = lambda output, term: output
    return kubectl, state, index


class TestTopCliIntegration:
    def test_no_flags_calls_kubectl_top_pods(self):
        kubectl, state, index = _make_mocks()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["top"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["top", "pods"])

    def test_extra_flags_reach_kubectl(self):
        kubectl, state, index = _make_mocks()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["top", "--sort-by=cpu"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["top", "pods", "--sort-by=cpu"])

    def test_match_filters_before_indexing(self):
        kubectl, state, index = _make_mocks()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["top", "--match", "ngi"])
        assert result.exit_code == 0
        index.filter.assert_called_once()

    def test_no_limits_flag_reaches_command(self):
        kubectl, state, index = _make_mocks()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
            patch("kx.main.TopCommand") as MockTopCommand,
        ):
            MockTopCommand.return_value.execute.return_value = "1  nginx"
            result = runner.invoke(app, ["top", "--no-limits"])
        assert result.exit_code == 0
        MockTopCommand.return_value.execute.assert_called_once_with(
            None, [], no_limits=True
        )

    def test_metrics_api_missing_renders_styled_error(self):
        kubectl, state, index = _make_mocks()
        kubectl.run.side_effect = RuntimeError("error: Metrics API not available")
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["top"])
        assert result.exit_code == 1
        assert "Metrics API not available" in result.output
