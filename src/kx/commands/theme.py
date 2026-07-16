from typing import Callable

from kx.themes import THEMES


class ThemeCommand:
    def __init__(self, save: Callable[[str], None]):
        self.save = save

    def execute(self, name: str) -> str:
        if name.isdigit():
            names = list(THEMES)
            position = int(name)
            if not 1 <= position <= len(names):
                raise ValueError(
                    f"Theme index {position} is out of range — {len(names)} themes "
                    f"(run 'kx theme' to list)."
                )
            name = names[position - 1]
        if name not in THEMES:
            raise ValueError(f"Unknown theme '{name}'. Run 'kx theme' to list themes.")
        self.save(name)
        return f"Theme set to '{name}'"
