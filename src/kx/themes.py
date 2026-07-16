from dataclasses import dataclass

from rich.theme import Theme


@dataclass(frozen=True, slots=True)
class Palette:
    accent: str
    muted: str
    body: str
    error: str
    warn: str
    success: str
    header: str | None = None  # defaults to f"bold {accent}"


DEFAULT_THEME = "github-dark"

THEMES: dict[str, Palette] = {
    "github-dark": Palette(
        accent="#3fb950",
        muted="#7d8590",
        body="#e6edf3",
        error="#f85149",
        warn="#e3b341",
        success="#3fb950",
    ),
    "dracula": Palette(
        accent="#bd93f9",
        muted="#6272a4",
        body="#f8f8f2",
        error="#ff5555",
        warn="#f1fa8c",
        success="#50fa7b",
    ),
    "nord": Palette(
        accent="#88c0d0",
        muted="#4c566a",
        body="#d8dee9",
        error="#bf616a",
        warn="#ebcb8b",
        success="#a3be8c",
    ),
    "gruvbox": Palette(
        accent="#fe8019",
        muted="#928374",
        body="#ebdbb2",
        error="#fb4934",
        warn="#fabd2f",
        success="#b8bb26",
    ),
    "solarized-dark": Palette(
        accent="#268bd2",
        muted="#586e75",
        body="#93a1a1",
        error="#dc322f",
        warn="#b58900",
        success="#859900",
    ),
    "catppuccin-mocha": Palette(
        accent="#cba6f7",
        muted="#6c7086",
        body="#cdd6f4",
        error="#f38ba8",
        warn="#f9e2af",
        success="#a6e3a1",
    ),
    "tokyo-night": Palette(
        accent="#7aa2f7",
        muted="#565f89",
        body="#c0caf5",
        error="#f7768e",
        warn="#e0af68",
        success="#9ece6a",
    ),
    "rose-pine": Palette(
        accent="#c4a7e7",
        muted="#6e6a86",
        body="#e0def4",
        error="#eb6f92",
        warn="#f6c177",
        success="#9ccfd8",
    ),
    "mono": Palette(
        accent="bold",
        muted="bright_black",
        body="default",
        error="bold",
        warn="default",
        success="bold",
        header="bold",
    ),
    "light": Palette(
        accent="#1a7f37",
        muted="#57606a",
        body="#24292f",
        error="#cf222e",
        warn="#9a6700",
        success="#1a7f37",
    ),
}

STYLE_KEYS = (
    "accent",
    "header",
    "muted",
    "body",
    "error",
    "warn",
    "success",
    "status.ok",
    "status.warn",
    "status.bad",
    "status.neutral",
)


def styles(name: str) -> dict[str, str]:
    """Expand a palette into the full semantic style mapping."""
    if name not in THEMES:
        raise ValueError(f"Unknown theme '{name}'. Run 'kx theme' to list themes.")
    palette = THEMES[name]
    return {
        "accent": palette.accent,
        "header": palette.header or f"bold {palette.accent}",
        "muted": palette.muted,
        "body": palette.body,
        "error": palette.error,
        "warn": palette.warn,
        "success": palette.success,
        "status.ok": palette.success,
        "status.warn": palette.warn,
        "status.bad": palette.error,
        "status.neutral": palette.body,
    }


def rich_theme(name: str) -> Theme:
    return Theme(styles(name))
