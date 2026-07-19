from kx.index import IndexServiceProtocol
from kx.kinds import normalize_kind
from kx.kubectl import KubectlServiceProtocol
from kx.state import Query, State, StateServiceProtocol


def _extract_namespace(extra_args: list[str]) -> str | None:
    for index, arg in enumerate(extra_args):
        if arg in ("-n", "--namespace") and index + 1 < len(extra_args):
            return extra_args[index + 1]
        if arg.startswith("--namespace="):
            return arg.split("=", 1)[1]
    return None


class GetCommand:
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
        resource: str,
        filter_term: str | None = None,
        extra_args: list[str] | None = None,
    ) -> str:
        extra_args = extra_args or []
        output = self.kubectl.run(["get", resource, *extra_args])
        if filter_term:
            output = self.index.filter(output, filter_term)
        all_namespaces = any(arg in ("-A", "--all-namespaces") for arg in extra_args)
        if all_namespaces:
            # Names aren't unique across namespaces, so `-A` results are never
            # indexed — returning unindexed output keeps dead X numbers off
            # the screen.
            return output
        indexed_output, names = self.index.add(output)
        if names:
            namespace = (
                _extract_namespace(extra_args) or self.kubectl.current_namespace()
            )
            kind = normalize_kind(resource)
            self.state.save(
                State(
                    resources={name: kind for name in names},
                    namespace=namespace,
                    query=Query(resource=resource, args=extra_args, match=filter_term),
                )
            )
        return indexed_output
