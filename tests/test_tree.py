from unittest.mock import MagicMock

from kx.commands.tree import TreeCommand
from kx.kinds import Kind
from kx.state import State


def _command(state, build_tree=None, build_indexed_tree=None):
    return TreeCommand(
        state=state,
        kubectl=MagicMock(),
        build_tree=build_tree or MagicMock(),
        build_indexed_tree=build_indexed_tree or MagicMock(),
    )


def test_non_indexed_uses_build_tree_and_does_not_save():
    state = MagicMock()
    state.fields.return_value = ("web", "default", Kind.Deployment)
    build_tree = MagicMock(return_value="TREE")
    build_indexed = MagicMock()
    cmd = _command(state, build_tree=build_tree, build_indexed_tree=build_indexed)

    result = cmd.execute(1)

    assert result == "TREE"
    build_tree.assert_called_once_with(Kind.Deployment, "web", "default")
    build_indexed.assert_not_called()
    state.save.assert_not_called()


def test_indexed_saves_returned_resources():
    state = MagicMock()
    state.fields.return_value = ("web", "default", Kind.Deployment)
    resources = [("web", Kind.Deployment), ("web-abc", Kind.ReplicaSet)]
    build_indexed = MagicMock(return_value=("TREE", resources))
    cmd = _command(state, build_indexed_tree=build_indexed)

    result = cmd.execute(1, indexed=True)

    assert result == "TREE"
    build_indexed.assert_called_once_with(Kind.Deployment, "web", "default")
    state.save.assert_called_once()
    saved = state.save.call_args.args[0]
    assert isinstance(saved, State)
    assert saved.namespace == "default"
    assert saved.resources == {"web": Kind.Deployment, "web-abc": Kind.ReplicaSet}


def test_indexed_with_no_resources_skips_save():
    state = MagicMock()
    state.fields.return_value = ("web", "default", Kind.Deployment)
    build_indexed = MagicMock(return_value=("TREE", []))
    cmd = _command(state, build_indexed_tree=build_indexed)

    result = cmd.execute(1, indexed=True)

    assert result == "TREE"
    state.save.assert_not_called()
