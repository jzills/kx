import json
import pytest
from unittest.mock import MagicMock

from kx.commands.metadata_write import _MetadataWriteCommand
from kx.kinds import Kind


def _make_command(
    verb="label", field="labels", name="nginx", namespace="default", kind=str(Kind.Pod)
):
    state = MagicMock()
    state.fields.return_value = (name, namespace, kind)
    kubectl = MagicMock()
    return (
        _MetadataWriteCommand(kubectl=kubectl, state=state, verb=verb, field=field),
        state,
        kubectl,
    )


def _current(data: dict) -> str:
    return json.dumps({"metadata": {"labels": data}})


class TestMetadataWriteCommandSet:
    def test_set_new_label(self):
        cmd, _, kubectl = _make_command()
        kubectl.run.side_effect = [_current({}), ""]
        result = cmd.execute(1, {"env": "prod"}, [], overwrite=False)
        assert kubectl.run.call_args_list[1][0][0] == [
            "label",
            str(Kind.Pod),
            "nginx",
            "-n",
            "default",
            "env=prod",
        ]
        assert result == "Labeled Pod/nginx (set 1)"

    def test_set_multiple_labels(self):
        cmd, _, kubectl = _make_command()
        kubectl.run.side_effect = [_current({}), ""]
        cmd.execute(1, {"env": "prod", "team": "platform"}, [], overwrite=False)
        args = kubectl.run.call_args_list[1][0][0]
        assert "env=prod" in args
        assert "team=platform" in args

    def test_conflict_without_overwrite_raises(self):
        cmd, _, kubectl = _make_command()
        kubectl.run.side_effect = [_current({"env": "staging"})]
        with pytest.raises(ValueError, match="env"):
            cmd.execute(1, {"env": "prod"}, [], overwrite=False)
        assert kubectl.run.call_count == 1  # no mutating call was made

    def test_conflict_with_overwrite_succeeds(self):
        cmd, _, kubectl = _make_command()
        kubectl.run.side_effect = [_current({"env": "staging"}), ""]
        result = cmd.execute(1, {"env": "prod"}, [], overwrite=True)
        args = kubectl.run.call_args_list[1][0][0]
        assert "env=prod" in args
        assert "--overwrite" in args
        assert result == "Labeled Pod/nginx (set 1)"


class TestMetadataWriteCommandRemove:
    def test_remove_label(self):
        cmd, _, kubectl = _make_command()
        kubectl.run.side_effect = [_current({"env": "prod"}), ""]
        result = cmd.execute(1, {}, ["env"], overwrite=False)
        args = kubectl.run.call_args_list[1][0][0]
        assert "env-" in args
        assert result == "Labeled Pod/nginx (removed 1)"

    def test_remove_absent_key_not_an_error(self):
        cmd, _, kubectl = _make_command()
        kubectl.run.side_effect = [_current({}), ""]
        result = cmd.execute(1, {}, ["missing"], overwrite=False)
        assert result == "Labeled Pod/nginx (removed 1)"


class TestMetadataWriteCommandSetAndRemove:
    def test_set_and_remove_together(self):
        cmd, _, kubectl = _make_command()
        kubectl.run.side_effect = [_current({}), ""]
        result = cmd.execute(1, {"env": "prod"}, ["stale"], overwrite=False)
        args = kubectl.run.call_args_list[1][0][0]
        assert "env=prod" in args
        assert "stale-" in args
        assert result == "Labeled Pod/nginx (set 1, removed 1)"


class TestMetadataWriteCommandValidation:
    def test_empty_invocation_raises(self):
        cmd, _, kubectl = _make_command()
        with pytest.raises(ValueError, match="nothing to set or remove"):
            cmd.execute(1, {}, [], overwrite=False)
        kubectl.run.assert_not_called()


class TestMetadataWriteCommandAnnotate:
    def test_annotate_verb_and_field(self):
        cmd, _, kubectl = _make_command(verb="annotate", field="annotations")
        kubectl.run.side_effect = [
            json.dumps({"metadata": {"annotations": {}}}),
            "",
        ]
        result = cmd.execute(1, {"note": "x"}, [], overwrite=False)
        args = kubectl.run.call_args_list[1][0][0]
        assert args[0] == "annotate"
        assert "note=x" in args
        assert result == "Annotated Pod/nginx (set 1)"
