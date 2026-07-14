import os
import tomllib
from dataclasses import dataclass
from pathlib import Path

_CONFIG_FILE = Path.home() / ".kx" / "config.toml"


@dataclass(frozen=True, slots=True)
class Config:
    max_history: int = 10
    shells: tuple[str, ...] = ("bash", "sh")
    no_color: bool = False


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

    if "KX_MAX_HISTORY" in os.environ:
        try:
            kwargs["max_history"] = int(os.environ["KX_MAX_HISTORY"])
        except ValueError:
            raise SystemExit("kx: KX_MAX_HISTORY must be an integer")

    if "KX_SHELLS" in os.environ:
        kwargs["shells"] = tuple(os.environ["KX_SHELLS"].split(","))

    return Config(**kwargs)
