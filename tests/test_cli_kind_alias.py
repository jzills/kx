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

    def test_index_after_kind_resolves_to_name(self):
        kubectl, state, index = _make_mocks()
        state.fields.return_value = ("curl", "default", "Pod")
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["po", "3"])
        assert result.exit_code == 0
        state.fields.assert_called_once_with(3)
        kubectl.run.assert_called_once_with(["get", "po", "curl", "-n", "default"])

    def test_explicit_get_with_index_resolves_too(self):
        kubectl, state, index = _make_mocks()
        state.fields.return_value = ("curl", "default", "Pod")
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["get", "po", "3"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["get", "po", "curl", "-n", "default"])

    def test_multiple_indexes_resolve_to_names(self):
        kubectl, state, index = _make_mocks()
        state.fields.side_effect = [
            ("curl", "default", "Pod"),
            ("echo", "default", "Pod"),
        ]
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["po", "1", "2"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(
            ["get", "po", "curl", "echo", "-n", "default"]
        )

    def test_explicit_namespace_flag_not_overridden(self):
        kubectl, state, index = _make_mocks()
        state.fields.return_value = ("curl", "default", "Pod")
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["po", "3", "-n", "other"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["get", "po", "curl", "-n", "other"])

    def test_kind_mismatch_errors(self):
        kubectl, state, index = _make_mocks()
        state.fields.return_value = ("web-healthy", "default", "Deployment")
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["po", "3"])
        assert result.exit_code == 1
        assert "Deployment/web-healthy" in result.output
        kubectl.run.assert_not_called()

    def test_unknown_token_still_errors(self):
        result = runner.invoke(app, ["nonsense"])
        assert result.exit_code != 0

    def test_command_typo_still_errors_not_aliased(self):
        result = runner.invoke(app, ["descrbe", "2"])
        assert result.exit_code != 0
