import pytest
from rich.style import Style
from rich.theme import Theme

from kx.themes import DEFAULT_THEME, STYLE_KEYS, THEMES, rich_theme, styles


def test_default_theme_registered():
    assert DEFAULT_THEME in THEMES


def test_every_theme_defines_every_style_key():
    for name in THEMES:
        assert set(styles(name)) == set(STYLE_KEYS)


def test_every_style_value_parses():
    for name in THEMES:
        for key, value in styles(name).items():
            Style.parse(value)


def test_unknown_theme_raises_value_error():
    with pytest.raises(ValueError, match="'nope'"):
        styles("nope")
    with pytest.raises(ValueError, match="'nope'"):
        rich_theme("nope")


def test_rich_theme_returns_theme():
    assert isinstance(rich_theme(DEFAULT_THEME), Theme)


def test_default_palette_locked_to_current_look():
    assert THEMES["github-dark"].accent == "#3fb950"
    assert styles("github-dark")["header"] == "bold #3fb950"
