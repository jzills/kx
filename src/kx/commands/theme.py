from typing import Callable

from kx.themes import THEMES


class ThemeCommand:
    def __init__(self, save: Callable[[str], None]):
        self.save = save

    def execute(self, name: str) -> str:
        if name not in THEMES:
            raise ValueError(f"Unknown theme '{name}'. Run 'kx theme' to list themes.")
        self.save(name)
        return f"Theme set to '{name}'"
