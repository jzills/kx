import base64
import json
from unittest.mock import MagicMock, patch

from typer.testing import CliRunner

from kx.main import app

runner = CliRunner()

_SECRET = {
    "data": {
        "username": base64.b64encode(b"admin").decode(),
        "password": base64.b64encode(b"s3cr3t").decode(),
    }
}


def _mocks(payload=None):
    kubectl = MagicMock()
    kubectl.run.return_value = json.dumps(payload if payload is not None else _SECRET)
    kubectl.current_namespace.return_value = "default"
    state = MagicMock()
    state.fields.return_value = ("db-credentials", "default", "Secret")
    index = MagicMock()
    return kubectl, state, index


def _invoke(args, payload=None):
    kubectl, state, index = _mocks(payload)
    with (
        patch("kx.main._kubectl", kubectl),
        patch("kx.main._state", state),
        patch("kx.main._index", index),
    ):
        result = runner.invoke(app, args)
    return result, kubectl, state


class TestDecodeRendering:
    def test_renders_decoded_key_value_table(self):
        result, _, _ = _invoke(["secret", "1", "--decode"])
        assert result.exit_code == 0
        assert "username" in result.output
        assert "admin" in result.output
        assert "s3cr3t" in result.output

    def test_shows_banner_with_item_count(self):
        result, _, _ = _invoke(["secret", "1", "--decode"])
        assert "Secret/db-credentials" in result.output
        assert "2 items" in result.output

    def test_singular_item_count(self):
        payload = {"data": {"username": base64.b64encode(b"admin").decode()}}
        result, _, _ = _invoke(["secret", "1", "--decode"], payload)
        assert "1 item" in result.output
        assert "1 items" not in result.output

    def test_empty_secret_reports_no_keys(self):
        result, _, _ = _invoke(["secret", "1", "--decode"], {"data": {}})
        assert result.exit_code == 0
        assert "No keys" in result.output

    def test_binary_value_shows_placeholder(self):
        payload = {
            "data": {"store.p12": base64.b64encode(b"\xff\xfe\x00\x01").decode()}
        }
        result, _, _ = _invoke(["secret", "1", "--decode"], payload)
        assert "<binary, 4 bytes>" in result.output

    def test_multiple_indexes_stack(self):
        result, _, _ = _invoke(["secret", "1", "2", "--decode"])
        assert result.exit_code == 0
        assert result.output.count("Secret/db-credentials") == 2

    def test_decode_does_not_save_state(self):
        _, _, state = _invoke(["secret", "1", "--decode"])
        state.save.assert_not_called()


class TestDecodeSpellings:
    def test_get_secret_spelling(self):
        result, _, _ = _invoke(["get", "secret", "1", "--decode"])
        assert result.exit_code == 0
        assert "s3cr3t" in result.output

    def test_kind_alias_singular_spelling(self):
        result, _, _ = _invoke(["secret", "1", "--decode"])
        assert result.exit_code == 0
        assert "s3cr3t" in result.output

    def test_kind_alias_plural_spelling(self):
        result, _, _ = _invoke(["secrets", "1", "--decode"])
        assert result.exit_code == 0
        assert "s3cr3t" in result.output

    def test_decode_flag_does_not_leak_to_kubectl(self):
        _, kubectl, _ = _invoke(["secret", "1", "--decode"])
        for call in kubectl.run.call_args_list:
            assert "--decode" not in call.args[0]


class TestDecodeValidation:
    def test_decode_without_index_errors(self):
        result, _, _ = _invoke(["secret", "--decode"])
        assert result.exit_code == 1
        assert "needs an index" in result.output

    def test_decode_on_non_secret_kind_errors(self):
        result, _, _ = _invoke(["pods", "1", "--decode"])
        assert result.exit_code == 1
        assert "only applies to Secrets" in result.output
        assert "Pod" in result.output

    def test_key_without_decode_errors(self):
        result, _, _ = _invoke(["secret", "1", "--key", "password"])
        assert result.exit_code == 1
        assert "--key requires --decode" in result.output

    def test_key_with_multiple_indexes_errors(self):
        result, _, _ = _invoke(["secret", "1", "2", "--decode", "--key", "password"])
        assert result.exit_code == 1
        assert "single index" in result.output


class TestKeyExtraction:
    def test_prints_raw_value_only(self):
        result, _, _ = _invoke(["secret", "1", "--decode", "--key", "password"])
        assert result.exit_code == 0
        assert result.output == "s3cr3t\n"

    def test_short_flag(self):
        result, _, _ = _invoke(["secret", "1", "--decode", "-k", "username"])
        assert result.exit_code == 0
        assert result.output == "admin\n"

    def test_long_value_is_not_wrapped(self):
        # Rich wraps at the console width (1000 off-terminal); a wrapped value
        # would put a newline inside $(kx secret … -k …) and corrupt it.
        long_value = "x" * 2000
        payload = {"data": {"cert": base64.b64encode(long_value.encode()).decode()}}
        result, _, _ = _invoke(["secret", "1", "--decode", "-k", "cert"], payload)
        assert result.exit_code == 0
        assert result.output == long_value + "\n"

    def test_binary_value_written_byte_exact(self):
        blob = b"\xff\xfe\x00\x01binary"
        payload = {"data": {"store.p12": base64.b64encode(blob).decode()}}
        result, _, _ = _invoke(["secret", "1", "--decode", "-k", "store.p12"], payload)
        assert result.exit_code == 0
        # No trailing newline: a redirect must reproduce the file exactly.
        assert result.stdout_bytes == blob

    def test_missing_key_errors(self):
        result, _, _ = _invoke(["secret", "1", "--decode", "--key", "nope"])
        assert result.exit_code == 1
        assert "No key 'nope'" in result.output
        assert "Secret/db-credentials" in result.output
