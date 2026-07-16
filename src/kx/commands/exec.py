import subprocess

from kx.kinds import Kind
from kx.kubectl import KubectlServiceProtocol
from kx.refresh import ensure_exists
from kx.state import StateServiceProtocol


class ExecCommand:
    def __init__(
        self,
        state: StateServiceProtocol,
        kubectl: KubectlServiceProtocol,
        shells: tuple[str, ...] = ("bash", "sh"),
    ):
        self.state = state
        self.kubectl = kubectl
        self.shells = shells

    def execute(
        self, index: int, cmd: list[str] | None, extra_args: list[str] | None = None
    ) -> None:
        extra_args = extra_args or []
        name, namespace, kind = self.state.fields(index)
        if kind != Kind.Pod:
            raise ValueError("exec is only supported for pods.")
        if cmd:
            rc = self.kubectl.run_interactive(
                ["exec", "-it", name, "-n", namespace, *extra_args, "--", *cmd],
                stderr=subprocess.DEVNULL,
            )
            if rc != 0:
                ensure_exists(self.kubectl, str(Kind.Pod), name, namespace)
                raise ValueError(f"Command failed in container (exit {rc}).")
        else:
            for shell in self.shells:
                probe_rc = self.kubectl.probe(
                    [
                        "exec",
                        name,
                        "-n",
                        namespace,
                        *extra_args,
                        "--",
                        shell,
                        "-c",
                        "exit 0",
                    ]
                )
                if probe_rc == 0:
                    self.kubectl.run_interactive(
                        ["exec", "-it", name, "-n", namespace, *extra_args, "--", shell]
                    )
                    return
            ensure_exists(self.kubectl, str(Kind.Pod), name, namespace)
            raise ValueError(
                "No shell found in container. Provide an explicit command: kx exec <index> -- /path/to/binary"
            )
