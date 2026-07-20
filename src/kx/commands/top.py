from kx.commands.get import _extract_namespace
from kx.index import IndexServiceProtocol
from kx.kinds import Kind
from kx.kubectl import KubectlServiceProtocol
from kx.state import Query, State, StateServiceProtocol


class TopCommand:
    def __init__(
        self,
        kubectl: KubectlServiceProtocol,
        state: StateServiceProtocol,
        index: IndexServiceProtocol,
    ):
        self.kubectl = kubectl
        self.state = state
        self.index = index

    def execute(
        self,
        filter_term: str | None = None,
        extra_args: list[str] | None = None,
    ) -> str:
        extra_args = extra_args or []
        output = self.kubectl.run(["top", "pods", *extra_args])
        if filter_term:
            output = self.index.filter(output, filter_term)
        all_namespaces = any(arg in ("-A", "--all-namespaces") for arg in extra_args)
        if all_namespaces:
            # Names aren't unique across namespaces — matches kx get's rule.
            return output
        indexed_output, names = self.index.add(output)
        if names:
            namespace = (
                _extract_namespace(extra_args) or self.kubectl.current_namespace()
            )
            self.state.save(
                State(
                    resources={name: Kind.Pod for name in names},
                    namespace=namespace,
                    query=Query(resource="pods", args=extra_args, match=filter_term),
                )
            )
        return indexed_output
