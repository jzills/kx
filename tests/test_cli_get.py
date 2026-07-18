from unittest.mock import MagicMock, patch

from typer.testing import CliRunner

from kx.main import app

runner = CliRunner()


def _make_mocks(kubectl_output="NAME\nnginx"):
    kubectl = MagicMock()
    kubectl.run.return_value = kubectl_output
    kubectl.current_namespace.return_value = "default"
    state = MagicMock()
    index = MagicMock()
    index.add.return_value = ("1  nginx", ["nginx"])
    index.filter.side_effect = lambda output, term: output
    return kubectl, state, index


class TestGetCliIntegration:
    def test_no_flags_calls_kubectl_without_namespace(self):
        kubectl, state, index = _make_mocks()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["get", "po"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["get", "po"])

    def test_sort_by_reaches_kubectl(self):
        kubectl, state, index = _make_mocks()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["get", "po", "--sort-by=.metadata.name"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["get", "po", "--sort-by=.metadata.name"])

    def test_explicit_namespace_reaches_kubectl(self):
        kubectl, state, index = _make_mocks()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["get", "po", "-n", "kube-system"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["get", "po", "-n", "kube-system"])

    def test_all_namespaces_reaches_kubectl(self):
        kubectl, state, index = _make_mocks()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["get", "po", "-A"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["get", "po", "-A"])
        state.save.assert_not_called()

    def test_all_namespaces_shows_signpost_without_indexes(self):
        kubectl, state, index = _make_mocks(
            kubectl_output="NAMESPACE   NAME\ndefault     nginx"
        )
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["get", "po", "-A"])
        assert result.exit_code == 0
        index.add.assert_not_called()
        assert "indexes not saved for all-namespace listings" in result.output

    def test_filter_and_extra_args_combined(self):
        kubectl, state, index = _make_mocks()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(
                app, ["get", "po", "--match", "nginx", "--sort-by=.metadata.name"]
            )
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["get", "po", "--sort-by=.metadata.name"])
        index.filter.assert_called_once_with("NAME\nnginx", "nginx")
