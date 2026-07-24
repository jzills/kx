import base64
import json

from kx.kubectl import KubectlServiceProtocol
from kx.state import StateServiceProtocol


def to_display(value: bytes) -> str:
    """Decoded text, or a placeholder for values that aren't valid UTF-8.

    Keeps keystores and other binary payloads from garbling the table; `--key`
    still emits their raw bytes so they can be redirected to a file."""
    try:
        return value.decode("utf-8")
    except UnicodeDecodeError:
        return f"<binary, {len(value)} bytes>"


def _decode_data(obj: dict) -> dict[str, bytes]:
    """Decoded `data` for one Secret object. Values stay `bytes` so the render
    layer owns the text/binary decision. `stringData` is write-only and never
    returned by the API, so `data` holds everything."""
    return {
        key: base64.b64decode(value) for key, value in (obj.get("data") or {}).items()
    }


class SecretCommand:
    def __init__(self, state: StateServiceProtocol, kubectl: KubectlServiceProtocol):
        self.state = state
        self.kubectl = kubectl

    def execute(self, index: int) -> dict[str, bytes]:
        """Decoded data for the indexed Secret."""
        name, namespace, kind = self.state.fields(index)
        raw = self.kubectl.run(
            ["get", kind, name, "-n", namespace, "-o", "json"],
        )
        return _decode_data(json.loads(raw))

    def execute_all(
        self, extra_args: list[str] | None = None
    ) -> list[tuple[str, str, dict[str, bytes]]]:
        """Every Secret in the target namespace as (name, namespace, data).

        One kubectl call returns the whole list with data attached, so a
        namespace sweep costs the same as a single fetch. `extra_args` carries
        the user's kubectl flags so `-n` selects the namespace."""
        raw = self.kubectl.run(["get", "secret", "-o", "json", *(extra_args or [])])
        obj = json.loads(raw)
        return [
            (
                item.get("metadata", {}).get("name", ""),
                item.get("metadata", {}).get("namespace", ""),
                _decode_data(item),
            )
            for item in obj.get("items") or []
        ]
