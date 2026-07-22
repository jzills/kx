from kx.kubectl import KubectlServiceProtocol
from kx.state import StateServiceProtocol


class ContextCommand:
    def __init__(self, state: StateServiceProtocol, kubectl: KubectlServiceProtocol):
        self.state = state
        self.kubectl = kubectl

    def execute(self, index: int) -> str:
        name, _, _ = self.state.fields(index)
        self.kubectl.run(["config", "use-context", name])
        return name
