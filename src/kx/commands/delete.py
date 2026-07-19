from contextlib import nullcontext

from kx.kubectl import KubectlServiceProtocol
from kx.state import StateServiceProtocol
from kx.types import Confirm, Status


class DeleteCommand:
    def __init__(
        self,
        state: StateServiceProtocol,
        kubectl: KubectlServiceProtocol,
        confirm: Confirm,
        status: Status | None = None,
    ):
        self.state = state
        self.kubectl = kubectl
        self.confirm = confirm
        self.status = status or (lambda _msg: nullcontext())

    def execute(self, index: int, yes: bool) -> str:
        name, namespace, kind = self.state.fields(index)
        # The confirm prompt must stay outside the spinner: a prompt inside a
        # Live region breaks input.
        if not yes:
            self.confirm(f"Delete {kind}/{name} in {namespace}?")
        with self.status("deleting"):
            self.kubectl.run(["delete", kind, name, "-n", namespace])
        return f"Deleted {kind}/{name}"
