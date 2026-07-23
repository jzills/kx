import yaml

from kx.kubectl import KubectlServiceProtocol
from kx.state import StateServiceProtocol


def _find_keys(data: dict | list, keys: set[str]) -> dict:
    """Collect the value of each requested key from anywhere in the manifest,
    preferring the shallowest occurrence when a key appears at multiple depths.

    A breadth-first walk guarantees shallowest-wins: a workload's own top-level
    `metadata` is returned rather than its pod template's nested `metadata`,
    while genuinely nested-only keys (e.g. `containerStatuses` under `status`)
    are still found. A matched key's own subtree is not descended into."""
    result: dict = {}
    frontier: list = [data]
    while frontier:
        deeper: list = []
        for node in frontier:
            if isinstance(node, dict):
                for k, v in node.items():
                    if k in keys:
                        result.setdefault(k, v)
                    else:
                        deeper.append(v)
            elif isinstance(node, list):
                deeper.extend(node)
        frontier = deeper
    return result


class YamlCommand:
    def __init__(self, state: StateServiceProtocol, kubectl: KubectlServiceProtocol):
        self.state = state
        self.kubectl = kubectl

    def execute(self, index: int, show: list[str] | None = None) -> str:
        name, namespace, kind = self.state.fields(index)
        raw = self.kubectl.run(["get", kind, name, "-n", namespace, "-o", "yaml"])
        if not show:
            return raw
        data = yaml.safe_load(raw)
        return yaml.dump(_find_keys(data, set(show)), default_flow_style=False)
