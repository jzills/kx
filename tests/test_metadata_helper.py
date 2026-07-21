import json
from unittest.mock import MagicMock

from kx.commands._metadata import fetch_metadata_field
from kx.kinds import Kind


def _make(name="nginx", namespace="default", kind=str(Kind.Pod)):
    state = MagicMock()
    state.fields.return_value = (name, namespace, kind)
    kubectl = MagicMock()
    return state, kubectl


class TestFetchMetadataField:
    def test_returns_field_dict(self):
        state, kubectl = _make()
        kubectl.run.return_value = json.dumps(
            {"metadata": {"labels": {"app": "nginx"}}}
        )
        result = fetch_metadata_field(kubectl, state, 1, "labels")
        assert result == {"app": "nginx"}

    def test_kubectl_args(self):
        state, kubectl = _make(name="my-pod", namespace="staging")
        kubectl.run.return_value = json.dumps({"metadata": {}})
        fetch_metadata_field(kubectl, state, 2, "labels")
        kubectl.run.assert_called_once_with(
            ["get", str(Kind.Pod), "my-pod", "-n", "staging", "-o", "json"]
        )

    def test_uses_state_fields(self):
        state, kubectl = _make()
        kubectl.run.return_value = json.dumps({"metadata": {}})
        fetch_metadata_field(kubectl, state, 3, "labels")
        state.fields.assert_called_once_with(3)

    def test_missing_field_returns_empty_dict(self):
        state, kubectl = _make()
        kubectl.run.return_value = json.dumps({"metadata": {}})
        result = fetch_metadata_field(kubectl, state, 1, "labels")
        assert result == {}

    def test_null_field_returns_empty_dict(self):
        state, kubectl = _make()
        kubectl.run.return_value = json.dumps({"metadata": {"labels": None}})
        result = fetch_metadata_field(kubectl, state, 1, "labels")
        assert result == {}

    def test_annotations_field(self):
        state, kubectl = _make()
        kubectl.run.return_value = json.dumps(
            {"metadata": {"annotations": {"kubernetes.io/note": "x"}}}
        )
        result = fetch_metadata_field(kubectl, state, 1, "annotations")
        assert result == {"kubernetes.io/note": "x"}
