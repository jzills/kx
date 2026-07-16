from collections.abc import Callable
from contextlib import AbstractContextManager

from rich.tree import Tree

Confirm = Callable[[str], None]
Status = Callable[[str], AbstractContextManager]
BuildTree = Callable[[str, str, str], Tree]
BuildIndexedTree = Callable[[str, str, str], tuple[Tree, list[tuple[str, str]]]]
