from kx.commands.contexts import CONTEXT_KIND as _CONTEXT_KIND
from kx.kubectl import KubectlServiceProtocol
from kx.state import StateServiceProtocol


class ContextCommand:
    def __init__(self, state: StateServiceProtocol, kubectl: KubectlServiceProtocol):
        self.state = state
        self.kubectl = kubectl

    def execute(self, index: int) -> str:
        name, _, kind = self.state.fields(index)
        # ContextsCommand tags its entries "Context"; anything else means the
        # active state entry is a resource listing, not the context listing.
        if kind != _CONTEXT_KIND:
            raise ValueError(
                f"Index {index} is {kind}/{name}, not a Context. "
                f"Run `kx context` to list contexts, or `kx back` to return to "
                f"an earlier context listing."
            )
        self.kubectl.run(["config", "use-context", name])
        return name
