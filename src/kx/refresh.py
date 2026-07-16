"""Stale-state detection and recovery.

When a command fails because its indexed resource no longer exists (pod churn),
RefreshService re-runs the `kx get` query that produced the current state entry,
pushing the fresh list as a new history entry so the user can pick a new index.
The original command is never retried — the index→name mapping may have shifted.
"""

from kubernetes.client.exceptions import ApiException

from kx.commands.get import GetCommand
from kx.index import IndexServiceProtocol
from kx.kubectl import KubectlServiceProtocol
from kx.state import StateServiceProtocol


class StaleResourceError(RuntimeError):
    """A probe confirmed the indexed resource no longer exists."""


_NOT_FOUND_MARKERS = ("(NotFound)", "not found")


def ensure_exists(
    kubectl: KubectlServiceProtocol, kind: str, name: str, namespace: str
) -> None:
    if kubectl.probe(["get", kind, name, "-n", namespace]) != 0:
        raise StaleResourceError(f"{kind}/{name} no longer exists")


class RefreshService:
    def __init__(
        self,
        state: StateServiceProtocol,
        kubectl: KubectlServiceProtocol,
        index: IndexServiceProtocol,
    ):
        self.state = state
        self.kubectl = kubectl
        self.index = index

    def matches(self, error: BaseException) -> bool:
        if isinstance(error, StaleResourceError):
            return True
        if isinstance(error, ApiException):
            return error.status == 404
        if isinstance(error, RuntimeError):
            message = str(error)
            return any(marker in message for marker in _NOT_FOUND_MARKERS)
        return False

    def recover(self) -> tuple[str, str, str] | None:
        query = self.state.load().query
        if query is None:
            return None
        get = GetCommand(kubectl=self.kubectl, state=self.state, index=self.index)
        table = get.execute(query.resource, query.match, query.args)
        # Re-read after the refresh so the namespace reflects the new entry.
        return table, query.resource, self.state.load().namespace
