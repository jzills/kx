from kx.kinds import Kind, ensure_kind
from kx.kubectl import KubectlServiceProtocol
from kx.state import StateServiceProtocol


class NamespaceCommand:
    def __init__(self, state: StateServiceProtocol, kubectl: KubectlServiceProtocol):
        self.state = state
        self.kubectl = kubectl

    def execute(self, index: int) -> str:
        name, _, kind = self.state.fields(index)
        # kubectl config set-context accepts any string, so a stale index
        # pointing at a Pod would silently make its name the active namespace.
        ensure_kind(index, name, kind, Kind.Namespace)
        self.kubectl.run(["config", "set-context", "--current", f"--namespace={name}"])
        return name
