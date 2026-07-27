from unittest.mock import MagicMock, patch

import pytest
from typer.testing import CliRunner

from kx.commands.namespace import NamespaceCommand
from kx.main import app

runner = CliRunner()


def _make_command(namespace_name="production"):
    kubectl = MagicMock()
    state = MagicMock()
    state.fields.return_value = (namespace_name, "default", "Namespace")
    return NamespaceCommand(kubectl=kubectl, state=state), state, kubectl


class TestNamespaceCommandExecute:
    def test_resolves_index_from_state(self):
        cmd, state, _ = _make_command()
        cmd.execute(2)
        state.fields.assert_called_once_with(2)

    def test_sets_context_to_resolved_name(self):
        cmd, _, kubectl = _make_command("staging")
        cmd.execute(1)
        kubectl.run.assert_called_once_with(
            ["config", "set-context", "--current", "--namespace=staging"]
        )

    def test_returns_namespace_name(self):
        cmd, _, _ = _make_command("production")
        assert cmd.execute(1) == "production"

    def test_raises_on_invalid_index(self):
        kubectl = MagicMock()
        state = MagicMock()
        state.fields.side_effect = RuntimeError("Index out of range")
        cmd = NamespaceCommand(kubectl=kubectl, state=state)
        try:
            cmd.execute(99)
            assert False, "expected RuntimeError"
        except RuntimeError:
            pass

    def test_rejects_non_namespace_kind(self):
        kubectl = MagicMock()
        state = MagicMock()
        state.fields.return_value = ("dragonfly-0", "db", "Pod")
        cmd = NamespaceCommand(kubectl=kubectl, state=state)
        with pytest.raises(ValueError) as excinfo:
            cmd.execute(2)
        assert "not Namespace — run 'kx get ns' to relist." in str(excinfo.value)
        kubectl.run.assert_not_called()

    def test_raises_on_kubectl_error(self):
        kubectl = MagicMock()
        kubectl.run.side_effect = RuntimeError("kubectl failed")
        state = MagicMock()
        state.fields.return_value = ("production", "default", "Namespace")
        cmd = NamespaceCommand(kubectl=kubectl, state=state)
        try:
            cmd.execute(1)
            assert False, "expected RuntimeError"
        except RuntimeError:
            pass


class TestNamespaceCliBareForm:
    def _make_mocks(self):
        kubectl = MagicMock()
        kubectl.run.return_value = "NAME      STATUS\ndefault   Active"
        kubectl.current_namespace.return_value = "default"
        state = MagicMock()
        state.load.return_value.namespace = "default"
        index = MagicMock()
        index.add.return_value = ("1  default", ["default"])
        return kubectl, state, index

    def test_bare_namespace_lists_namespaces(self):
        kubectl, state, index = self._make_mocks()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["namespace"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["get", "namespaces"])
        state.save.assert_called_once()

    def test_bare_ns_alias_lists_namespaces(self):
        kubectl, state, index = self._make_mocks()
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["ns"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(["get", "namespaces"])

    def test_ns_with_index_still_switches(self):
        kubectl, state, index = self._make_mocks()
        state.fields.return_value = ("dev", None, "Namespace")
        with (
            patch("kx.main._kubectl", kubectl),
            patch("kx.main._state", state),
            patch("kx.main._index", index),
        ):
            result = runner.invoke(app, ["ns", "3"])
        assert result.exit_code == 0
        kubectl.run.assert_called_once_with(
            ["config", "set-context", "--current", "--namespace=dev"]
        )
