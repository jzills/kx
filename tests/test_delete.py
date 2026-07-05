from unittest.mock import MagicMock

from kx.commands.delete import DeleteCommand
from kx.kinds import Kind


def _command(state, kubectl, confirm):
    return DeleteCommand(state=state, kubectl=kubectl, confirm=confirm)


def test_with_yes_skips_confirmation():
    state = MagicMock()
    state.fields.return_value = ("web", "default", Kind.Pod)
    kubectl = MagicMock()
    confirm = MagicMock()

    message = _command(state, kubectl, confirm).execute(1, yes=True)

    confirm.assert_not_called()
    kubectl.run.assert_called_once_with(["delete", Kind.Pod, "web", "-n", "default"])
    assert message == "Deleted Pod/web"


def test_without_yes_prompts_then_deletes():
    state = MagicMock()
    state.fields.return_value = ("web", "default", Kind.Pod)
    kubectl = MagicMock()
    confirm = MagicMock()

    message = _command(state, kubectl, confirm).execute(1, yes=False)

    confirm.assert_called_once_with("Delete Pod/web in default?")
    kubectl.run.assert_called_once_with(["delete", Kind.Pod, "web", "-n", "default"])
    assert message == "Deleted Pod/web"
