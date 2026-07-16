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


def test_execute_by_index_saves_matching_theme():
    from kx.themes import THEMES

    saved = []
    command = ThemeCommand(save=saved.append)
    names = list(THEMES)
    assert command.execute("1") == f"Theme set to '{names[0]}'"
    assert command.execute(str(len(names))) == f"Theme set to '{names[-1]}'"
    assert saved == [names[0], names[-1]]


def test_execute_index_out_of_range_raises_without_saving():
    from kx.themes import THEMES

    saved = []
    command = ThemeCommand(save=saved.append)
    for bad in ("0", str(len(THEMES) + 1)):
        with pytest.raises(ValueError, match="out of range"):
            command.execute(bad)
    assert saved == []
