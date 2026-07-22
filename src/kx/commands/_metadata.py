import json

from kx.kubectl import KubectlServiceProtocol
from kx.state import StateServiceProtocol


def fetch_metadata_field(
    kubectl: KubectlServiceProtocol,
    state: StateServiceProtocol,
    index: int,
    field: str,
) -> dict[str, str]:
    name, namespace, kind = state.fields(index)
    raw = kubectl.run(["get", kind, name, "-n", namespace, "-o", "json"])
    obj = json.loads(raw)
    return obj.get("metadata", {}).get(field) or {}
