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


class TestKindShorthand:
    def test_kind_shorthand_matches_explicit_get(self):
        kubectl, state, index = _make_mocks()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["pods"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["get", "pods"])

    def test_shorthand_spelling_passes_through_unchanged(self):
        kubectl, state, index = _make_mocks()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["deploy"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["get", "deploy"])

    def test_flags_reach_kubectl(self):
        kubectl, state, index = _make_mocks()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["deploy", "-n", "kube-system"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["get", "deploy", "-n", "kube-system"])

    def test_match_option_filters(self):
        kubectl, state, index = _make_mocks()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["svc", "--match", "api"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["get", "svc"])
        index.filter.assert_called_once_with("NAME\nnginx", "api")

    def test_registered_command_wins_over_kind_spelling(self):
        state = MagicMock()
        state.fields.return_value = ("dev", None, "Namespace")
        kubectl = MagicMock()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
        ):
            result = runner.invoke(app, ["ns", "3"])
        assert result.exit_code == 0
        state.fields.assert_called_once_with(3)
        kubectl.run.assert_called_once_with(
            ["config", "set-context", "--current", "--namespace=dev"]
        )

    def test_bare_integer_after_kind_errors_with_guidance(self):
        result = runner.invoke(app, ["po", "3"])
        assert result.exit_code != 0
        assert "doesn't take an index" in result.output
        assert "kx describe 3" in result.output

    def test_integer_guard_covers_flag_forms(self):
        result = runner.invoke(app, ["deploy", "-n", "kube-system", "3"])
        assert result.exit_code != 0
        assert "doesn't take an index" in result.output

    def test_explicit_get_with_integer_passes_through(self):
        kubectl, state, index = _make_mocks()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["get", "po", "3"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["get", "po", "3"])

    def test_unknown_token_still_errors(self):
        result = runner.invoke(app, ["nonsense"])
        assert result.exit_code != 0

    def test_command_typo_still_errors_not_aliased(self):
        result = runner.invoke(app, ["descrbe", "2"])
        assert result.exit_code != 0
