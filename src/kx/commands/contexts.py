from kx.index import IndexServiceProtocol
from kx.kubectl import KubectlServiceProtocol
from kx.state import State, StateServiceProtocol

# Pseudo-kind for kubeconfig contexts: not a Kubernetes kind, but stored in
# State.resources so ContextCommand can tell a context index from a resource.
CONTEXT_KIND = "Context"


class ContextsCommand:
    def __init__(
        self,
        kubectl: KubectlServiceProtocol,
        state: StateServiceProtocol,
        index: IndexServiceProtocol,
    ):
        self.kubectl = kubectl
        self.state = state
        self.index = index

    def execute(self) -> str:
        output = self.kubectl.run(["config", "get-contexts"])
        indexed_output, names = self.index.add(output)
        if names:
            current = self.kubectl.current_context()
            self.state.save(
                State(
                    resources={name: CONTEXT_KIND for name in names},
                    namespace=current,
                )
            )
        return indexed_output
