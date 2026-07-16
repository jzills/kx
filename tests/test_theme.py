import pytest

from kx.commands.theme import ThemeCommand


def test_execute_saves_and_reports():
    saved = []
    command = ThemeCommand(save=saved.append)
    assert command.execute("dracula") == "Theme set to 'dracula'"
    assert saved == ["dracula"]


def test_execute_unknown_theme_raises_without_saving():
    saved = []
    command = ThemeCommand(save=saved.append)
    with pytest.raises(ValueError, match="'bogus'"):
        command.execute("bogus")
    assert saved == []
