import json
from unittest.mock import MagicMock

from kx.commands.annotations import AnnotationsCommand
from kx.kinds import Kind


def _make_command(name="nginx", namespace="default", kind=str(Kind.Pod)):
    state = MagicMock()
    state.fields.return_value = (name, namespace, kind)
    kubectl = MagicMock()
    return AnnotationsCommand(state=state, kubectl=kubectl), state, kubectl


def _kubectl_response(annotations: dict) -> str:
    return json.dumps({"metadata": {"annotations": annotations}})


class TestAnnotationsCommand:
    def test_returns_annotations_dict(self):
        cmd, _, kubectl = _make_command()
        kubectl.run.return_value = _kubectl_response(
            {"kubernetes.io/note": "x", "team": "platform"}
        )
        result = cmd.execute(1)
        assert result == {"kubernetes.io/note": "x", "team": "platform"}

    def test_kubectl_args(self):
        cmd, _, kubectl = _make_command(
            name="my-pod", namespace="staging", kind=str(Kind.Pod)
        )
        kubectl.run.return_value = _kubectl_response({})
        cmd.execute(2)
        kubectl.run.assert_called_once_with(
            ["get", str(Kind.Pod), "my-pod", "-n", "staging", "-o", "json"]
        )

    def test_uses_state_fields(self):
        cmd, state, kubectl = _make_command()
        kubectl.run.return_value = _kubectl_response({})
        cmd.execute(3)
        state.fields.assert_called_once_with(3)

    def test_empty_annotations_returns_empty_dict(self):
        cmd, _, kubectl = _make_command()
        kubectl.run.return_value = json.dumps({"metadata": {}})
        result = cmd.execute(1)
        assert result == {}

    def test_null_annotations_returns_empty_dict(self):
        cmd, _, kubectl = _make_command()
        kubectl.run.return_value = json.dumps({"metadata": {"annotations": None}})
        result = cmd.execute(1)
        assert result == {}
