from rich.tree import Tree

from kx.kinds import Kind
from kx.kubectl import KubectlServiceProtocol
from kx.state import State, StateServiceProtocol
from kx.types import (
    BuildIndexedTree,
    BuildNamespaceIndexedTree,
    BuildNamespaceTree,
    BuildTree,
)


class TreeCommand:
    def __init__(
        self,
        state: StateServiceProtocol,
        kubectl: KubectlServiceProtocol,
        build_tree: BuildTree,
        build_indexed_tree: BuildIndexedTree,
        build_namespace_tree: BuildNamespaceTree,
        build_namespace_indexed_tree: BuildNamespaceIndexedTree,
    ):
        self.state = state
        self.kubectl = kubectl
        self.build_tree = build_tree
        self.build_indexed_tree = build_indexed_tree
        self.build_namespace_tree = build_namespace_tree
        self.build_namespace_indexed_tree = build_namespace_indexed_tree

    def execute(self, index: int, indexed: bool = False) -> Tree:
        name, namespace, kind = self.state.fields(index)
        # A Namespace row walks that namespace itself — its own name, not the
        # namespace the `kx get ns` was run in.
        if kind == Kind.Namespace:
            return self._namespace(name, indexed)
        if indexed:
            tree, resources = self.build_indexed_tree(kind, name, namespace)
            if resources:
                self.state.save(
                    State(
                        resources={name: kind for name, kind in resources},
                        namespace=namespace,
                    )
                )
        else:
            tree = self.build_tree(kind, name, namespace)
        return tree

    def execute_namespace(self, namespace: str, indexed: bool = False) -> Tree:
        return self._namespace(namespace, indexed)

    def _namespace(self, namespace: str, indexed: bool) -> Tree:
        if indexed:
            tree, resources = self.build_namespace_indexed_tree(namespace)
            if resources:
                self.state.save(
                    State(
                        resources={name: kind for name, kind in resources},
                        namespace=namespace,
                    )
                )
        else:
            tree = self.build_namespace_tree(namespace)
        return tree
