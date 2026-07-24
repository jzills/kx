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


class SecretCommand:
    def __init__(self, state: StateServiceProtocol, kubectl: KubectlServiceProtocol):
        self.state = state
        self.kubectl = kubectl

    def execute(self, index: int) -> dict[str, bytes]:
        """Decoded `data` for the indexed Secret. Values stay `bytes` so the
        render layer owns the text/binary decision. `stringData` is write-only
        and never returned by the API, so `data` holds everything."""
        name, namespace, kind = self.state.fields(index)
        raw = self.kubectl.run(
            ["get", kind, name, "-n", namespace, "-o", "json"],
        )
        obj = json.loads(raw)
        return {
            key: base64.b64decode(value)
            for key, value in (obj.get("data") or {}).items()
        }
