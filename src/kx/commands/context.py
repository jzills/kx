from kx.commands.contexts import CONTEXT_KIND as _CONTEXT_KIND
from kx.kinds import ensure_kind
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
        ensure_kind(index, name, kind, _CONTEXT_KIND, self.state)
        self.kubectl.run(["config", "use-context", name])
        return name
