from kx.kinds import Kind
from kx.kubectl import KubectlServiceProtocol
from kx.refresh import ensure_exists
from kx.state import StateServiceProtocol

_SUPPORTED_KINDS = {
    Kind.Pod,
    Kind.Deployment,
    Kind.ReplicaSet,
    Kind.StatefulSet,
    Kind.DaemonSet,
    Kind.Service,
}


class PortForwardCommand:
    def __init__(self, kubectl: KubectlServiceProtocol, state: StateServiceProtocol):
        self.kubectl = kubectl
        self.state = state

    def execute(
        self, index: int, port: str, extra_args: list[str] | None = None
    ) -> None:
        extra_args = extra_args or []
        name, namespace, kind = self.state.fields(index)
        if kind not in _SUPPORTED_KINDS:
            raise ValueError(f"port-forward is not supported for '{kind}'.")
        rc = self.kubectl.run_interactive(
            ["port-forward", f"{kind}/{name}", port, "-n", namespace, *extra_args]
        )
        if rc != 0:
            ensure_exists(self.kubectl, kind, name, namespace)
