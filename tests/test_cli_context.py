from unittest.mock import MagicMock, patch

from typer.testing import CliRunner

from kx.main import app

runner = CliRunner()


def _make_mocks():
    kubectl = MagicMock()
    kubectl.run.return_value = "CURRENT   NAME\n*         docker-desktop"
    kubectl.current_context.return_value = "docker-desktop"
    state = MagicMock()
    index = MagicMock()
    index.add.return_value = ("1  docker-desktop", ["docker-desktop"])
    return kubectl, state, index


class TestContextCliBareForm:
    def test_bare_context_lists_contexts(self):
        kubectl, state, index = _make_mocks()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["context"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["config", "get-contexts"])
        state.save.assert_called_once()

    def test_bare_contexts_alias_lists_contexts(self):
        kubectl, state, index = _make_mocks()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["contexts"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["config", "get-contexts"])

    def test_context_with_index_switches(self):
        kubectl, state, index = _make_mocks()
        state.fields.return_value = ("staging", None, "Context")
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["context", "3"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["config", "use-context", "staging"])


class TestGetContextsRouting:
    """`kx get contexts` is the relist hint a kind mismatch prints, so it has
    to list contexts rather than reach kubectl for a nonexistent resource."""

    def test_get_contexts_lists_contexts(self):
        kubectl, state, index = _make_mocks()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["get", "contexts"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["config", "get-contexts"])

    def test_get_context_singular_lists_contexts(self):
        kubectl, state, index = _make_mocks()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["get", "context"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["config", "get-contexts"])

    def test_get_contexts_with_index_switches(self):
        kubectl, state, index = _make_mocks()
        state.fields.return_value = ("staging", "default", "Context")
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["get", "contexts", "2"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["config", "use-context", "staging"])
