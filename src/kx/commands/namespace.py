from kx.kinds import Kind
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
        if kind != Kind.Namespace:
            raise ValueError(
                f"Index {index} is {kind}/{name}, not a Namespace. "
                f"Run `kx ns` to list namespaces, or `kx back` to return to "
                f"an earlier namespace listing."
            )
        self.kubectl.run(["config", "set-context", "--current", f"--namespace={name}"])
        return name
