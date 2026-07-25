import base64
import json
from unittest.mock import MagicMock

from kx.commands.secret import SecretCommand, to_display


def _encode(values: dict[str, bytes]) -> dict[str, str]:
    return {key: base64.b64encode(value).decode() for key, value in values.items()}


def _mocks(payload: dict):
    kubectl = MagicMock()
    kubectl.run.return_value = json.dumps(payload)
    state = MagicMock()
    state.fields.return_value = ("db-credentials", "default", "Secret")
    return kubectl, state


class TestSecretCommand:
    def test_decodes_data_values(self):
        kubectl, state = _mocks(
            {"data": _encode({"username": b"admin", "password": b"s3cr3t"})}
        )
        command = SecretCommand(state=state, kubectl=kubectl)
        assert command.execute(1) == {"username": b"admin", "password": b"s3cr3t"}

    def test_requests_json_for_the_indexed_secret(self):
        kubectl, state = _mocks({"data": {}})
        SecretCommand(state=state, kubectl=kubectl).execute(3)
        state.fields.assert_called_once_with(3)
        kubectl.run.assert_called_once_with(
            ["get", "Secret", "db-credentials", "-n", "default", "-o", "json"]
        )

    def test_missing_data_block_returns_empty(self):
        kubectl, state = _mocks({"metadata": {"name": "db-credentials"}})
        assert SecretCommand(state=state, kubectl=kubectl).execute(1) == {}

    def test_null_data_returns_empty(self):
        kubectl, state = _mocks({"data": None})
        assert SecretCommand(state=state, kubectl=kubectl).execute(1) == {}

    def test_preserves_binary_values_as_bytes(self):
        kubectl, state = _mocks({"data": _encode({"store.p12": b"\xff\xfe\x00\x01"})})
        result = SecretCommand(state=state, kubectl=kubectl).execute(1)
        assert result == {"store.p12": b"\xff\xfe\x00\x01"}


class TestSecretCommandNamespaceSweep:
    def test_returns_every_secret_with_its_namespace(self):
        payload = {
            "items": [
                {
                    "metadata": {"name": "db-credentials", "namespace": "default"},
                    "data": _encode({"password": b"s3cr3t"}),
                },
                {
                    "metadata": {"name": "tls-cert", "namespace": "default"},
                    "data": _encode({"tls.key": b"PRIVATE KEY"}),
                },
            ]
        }
        kubectl, state = _mocks(payload)
        assert SecretCommand(state=state, kubectl=kubectl).execute_all() == [
            ("db-credentials", "default", {"password": b"s3cr3t"}),
            ("tls-cert", "default", {"tls.key": b"PRIVATE KEY"}),
        ]

    def test_requests_json_for_the_whole_namespace(self):
        kubectl, state = _mocks({"items": []})
        SecretCommand(state=state, kubectl=kubectl).execute_all(["-n", "kube-system"])
        kubectl.run.assert_called_once_with(
            ["get", "secret", "-o", "json", "-n", "kube-system"]
        )

    def test_empty_namespace_returns_empty_list(self):
        kubectl, state = _mocks({"items": []})
        assert SecretCommand(state=state, kubectl=kubectl).execute_all() == []

    def test_secret_without_data_returns_empty_mapping(self):
        payload = {"items": [{"metadata": {"name": "empty", "namespace": "default"}}]}
        kubectl, state = _mocks(payload)
        assert SecretCommand(state=state, kubectl=kubectl).execute_all() == [
            ("empty", "default", {})
        ]


class TestToDisplay:
    def test_text_value_shows_plaintext(self):
        assert to_display(b"s3cr3t") == "s3cr3t"

    def test_multibyte_text_decodes(self):
        assert to_display("pässwörd".encode()) == "pässwörd"

    def test_binary_value_shows_placeholder(self):
        assert to_display(b"\xff\xfe\x00\x01") == "<binary, 4 bytes>"

    def test_empty_value_shows_empty_string(self):
        assert to_display(b"") == ""
