import os
import re
import tomllib
from dataclasses import dataclass
from pathlib import Path

from kx.themes import DEFAULT_THEME, THEMES

_CONFIG_FILE = Path.home() / ".kx" / "config.toml"


@dataclass(frozen=True, slots=True)
class Config:
    max_history: int = 10
    shells: tuple[str, ...] = ("bash", "sh")
    no_color: bool = False
    theme: str = DEFAULT_THEME


def load_config() -> Config:
    kwargs: dict = {}

    if _CONFIG_FILE.exists():
        try:
            with open(_CONFIG_FILE, "rb") as f:
                data = tomllib.load(f)
        except tomllib.TOMLDecodeError as e:
            raise SystemExit(f"kx: error reading {_CONFIG_FILE}: {e}")

        if "max_history" in data:
            kwargs["max_history"] = data["max_history"]
        if "shells" in data:
            kwargs["shells"] = tuple(data["shells"])
        if "no_color" in data:
            kwargs["no_color"] = data["no_color"]
        if "theme" in data:
            kwargs["theme"] = data["theme"]

    if "KX_MAX_HISTORY" in os.environ:
        try:
            kwargs["max_history"] = int(os.environ["KX_MAX_HISTORY"])
        except ValueError:
            raise SystemExit("kx: KX_MAX_HISTORY must be an integer")

    if "KX_SHELLS" in os.environ:
        kwargs["shells"] = tuple(os.environ["KX_SHELLS"].split(","))

    if "KX_NO_COLOR" in os.environ:
        value = os.environ["KX_NO_COLOR"].lower()
        kwargs["no_color"] = value in {"1", "true", "yes", "on"}

    if "KX_THEME" in os.environ:
        kwargs["theme"] = os.environ["KX_THEME"]

    if "max_history" in kwargs:
        mh = kwargs["max_history"]
        # bool is an int subclass; reject it and any non-int/non-positive value
        # (a string from TOML, 0, or a negative) with a clear message.
        if isinstance(mh, bool) or not isinstance(mh, int) or mh < 1:
            raise SystemExit("kx: max_history must be a positive integer")

    if "theme" in kwargs and kwargs["theme"] not in THEMES:
        raise SystemExit(
            f"kx: unknown theme '{kwargs['theme']}' (run kx theme to list themes)"
        )

    return Config(**kwargs)


def save_theme(name: str) -> None:
    """Persist the theme choice to the config file.

    Rewrites only the `theme = ...` line (or appends one) instead of
    round-tripping the TOML, so user comments and formatting survive. Safe
    because the config schema is flat: there are no tables a `theme` key
    could belong to.
    """
    line = f'theme = "{name}"'
    if _CONFIG_FILE.exists():
        text = _CONFIG_FILE.read_text()
        new, count = re.subn(r"(?m)^theme\s*=.*$", line, text, count=1)
        if count == 0:
            if text and not text.endswith("\n"):
                text += "\n"
            new = text + line + "\n"
        _CONFIG_FILE.write_text(new)
    else:
        _CONFIG_FILE.parent.mkdir(parents=True, exist_ok=True)
        _CONFIG_FILE.write_text(line + "\n")
