from unittest.mock import MagicMock

import pytest

from kx.commands.context import ContextCommand
from kx.state import State, StateHistory


def _make_command(context_name="production"):
    kubectl = MagicMock()
    state = MagicMock()
    state.fields.return_value = (context_name, "default", "Context")
    return ContextCommand(kubectl=kubectl, state=state), state, kubectl


def _history_ending_in_pods(previous=None):
    """Two entries with the cursor on the Pod listing, so the entry one step
    back is `previous` — the only thing the `kx back` clause consults."""
    return StateHistory(
        states=[
            State(resources=previous or {"web-0": "Pod"}, namespace="db"),
            State(resources={"dragonfly-0": "Pod"}, namespace="db"),
        ],
        cursor=1,
    )


class TestContextCommandExecute:
    def test_resolves_index_from_state(self):
        cmd, state, _ = _make_command()
        cmd.execute(2)
        state.fields.assert_called_once_with(2)

    def test_uses_context_to_resolved_name(self):
        cmd, _, kubectl = _make_command("staging")
        cmd.execute(1)
        kubectl.run.assert_called_once_with(["config", "use-context", "staging"])

    def test_returns_context_name(self):
        cmd, _, _ = _make_command("production")
        assert cmd.execute(1) == "production"

    def test_raises_on_invalid_index(self):
        kubectl = MagicMock()
        state = MagicMock()
        state.fields.side_effect = RuntimeError("Index out of range")
        cmd = ContextCommand(kubectl=kubectl, state=state)
        try:
            cmd.execute(99)
            assert False, "expected RuntimeError"
        except RuntimeError:
            pass

    def test_rejects_non_context_kind(self):
        kubectl = MagicMock()
        state = MagicMock()
        state.fields.return_value = ("dragonfly-0", "db", "Pod")
        state.load_history.return_value = _history_ending_in_pods()
        cmd = ContextCommand(kubectl=kubectl, state=state)
        with pytest.raises(ValueError) as excinfo:
            cmd.execute(2)
        assert "not Context — run 'kx get context' to relist." in str(excinfo.value)
        kubectl.run.assert_not_called()

    def test_offers_back_when_previous_entry_lists_contexts(self):
        kubectl = MagicMock()
        state = MagicMock()
        state.fields.return_value = ("dragonfly-0", "db", "Pod")
        state.load_history.return_value = _history_ending_in_pods(
            previous={"docker-desktop": "Context"}
        )
        cmd = ContextCommand(kubectl=kubectl, state=state)
        with pytest.raises(ValueError) as excinfo:
            cmd.execute(2)
        assert (
            "run 'kx get context' to relist, or 'kx back' for the previous "
            "Context listing." in str(excinfo.value)
        )
        kubectl.run.assert_not_called()

    def test_raises_on_kubectl_error(self):
        kubectl = MagicMock()
        kubectl.run.side_effect = RuntimeError("kubectl failed")
        state = MagicMock()
        state.fields.return_value = ("production", "default", "Context")
        cmd = ContextCommand(kubectl=kubectl, state=state)
        try:
            cmd.execute(1)
            assert False, "expected RuntimeError"
        except RuntimeError:
            pass
