from unittest.mock import MagicMock

from kx.commands.contexts import ContextsCommand
from kx.state import State


def _make_command():
    kubectl = MagicMock()
    state = MagicMock()
    index = MagicMock()
    return (
        ContextsCommand(kubectl=kubectl, state=state, index=index),
        kubectl,
        state,
        index,
    )


class TestContextsCommandExecute:
    def test_runs_get_contexts(self):
        cmd, kubectl, state, index = _make_command()
        kubectl.run.return_value = "CURRENT   NAME             CLUSTER\n*         docker-desktop   docker-desktop"
        index.add.return_value = ("1  docker-desktop", ["docker-desktop"])
        kubectl.current_context.return_value = "docker-desktop"
        cmd.execute()
        kubectl.run.assert_called_once_with(["config", "get-contexts"])

    def test_returns_indexed_output(self):
        cmd, kubectl, state, index = _make_command()
        kubectl.run.return_value = "CURRENT   NAME\n*         docker-desktop"
        index.add.return_value = ("X  NAME\n1  docker-desktop", ["docker-desktop"])
        kubectl.current_context.return_value = "docker-desktop"
        result = cmd.execute()
        assert result == "X  NAME\n1  docker-desktop"

    def test_saves_state_with_context_kind(self):
        cmd, kubectl, state, index = _make_command()
        kubectl.run.return_value = (
            "CURRENT   NAME\n*         docker-desktop\n          minikube"
        )
        index.add.return_value = (
            "X  NAME\n1  docker-desktop\n2  minikube",
            ["docker-desktop", "minikube"],
        )
        kubectl.current_context.return_value = "docker-desktop"
        cmd.execute()
        state.save.assert_called_once()
        saved: State = state.save.call_args[0][0]
        assert saved.resources == {"docker-desktop": "Context", "minikube": "Context"}
        assert saved.namespace == "docker-desktop"
        assert saved.query is None

    def test_no_names_does_not_save_state(self):
        cmd, kubectl, state, index = _make_command()
        kubectl.run.return_value = ""
        index.add.return_value = ("", [])
        cmd.execute()
        state.save.assert_not_called()
