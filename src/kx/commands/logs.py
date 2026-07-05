import json

from kx.kinds import Kind
from kx.kubectl import KubectlServiceProtocol
from kx.state import StateServiceProtocol

_AGGREGATE_KINDS = {Kind.Deployment, Kind.StatefulSet, Kind.DaemonSet, Kind.Service}


class LogsCommand:
    def __init__(self, state: StateServiceProtocol, kubectl: KubectlServiceProtocol):
        self.state = state
        self.kubectl = kubectl

    def execute(self, index: int, extra_args: list[str] | None = None) -> None:
        extra_args = extra_args or []
        name, namespace, kind = self.state.fields(index)
        if kind == Kind.Pod:
            self.kubectl.run_interactive(["logs", name, "-n", namespace, *extra_args])
        elif kind in _AGGREGATE_KINDS:
            selector = self._selector(name, namespace, kind)
            self.kubectl.run_interactive(
                ["logs", "-l", selector, "--prefix=true", "-n", namespace, *extra_args]
            )
        else:
            raise ValueError(f"Logs are not supported for '{kind}'.")

    def _selector(self, name: str, namespace: str, kind: str) -> str:
        raw = self.kubectl.run(["get", kind, name, "-n", namespace, "-o", "json"])
        obj = json.loads(raw)
        labels = self._extract_labels(obj, kind)
        if not labels:
            raise ValueError(
                f"{kind}/{name} has no pod selector; cannot aggregate logs."
            )
        return ",".join(f"{k}={v}" for k, v in labels.items())

    @staticmethod
    def _extract_labels(obj: dict, kind: str) -> dict:
        spec = obj.get("spec", {})
        if kind == Kind.Service:
            return spec.get("selector") or {}
        return (spec.get("selector") or {}).get("matchLabels") or {}
