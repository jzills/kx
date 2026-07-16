from kx.kubectl import KubectlServiceProtocol
from kx.refresh import ensure_exists
from kx.state import StateServiceProtocol


class EditCommand:
    def __init__(self, state: StateServiceProtocol, kubectl: KubectlServiceProtocol):
        self.state = state
        self.kubectl = kubectl

    def execute(self, index: int, extra_args: list[str] | None = None) -> None:
        extra_args = extra_args or []
        name, namespace, kind = self.state.fields(index)
        rc = self.kubectl.run_interactive(
            ["edit", kind, name, "-n", namespace, *extra_args]
        )
        if rc != 0:
            ensure_exists(self.kubectl, kind, name, namespace)
