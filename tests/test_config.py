import pytest
from unittest.mock import patch
from kx.config import load_config, save_theme


def _patch_config_file(path):
    return patch("kx.config._CONFIG_FILE", path)


def _clear_env(monkeypatch):
    monkeypatch.delenv("KX_MAX_HISTORY", raising=False)
    monkeypatch.delenv("KX_SHELLS", raising=False)
    monkeypatch.delenv("KX_NO_COLOR", raising=False)
    monkeypatch.delenv("KX_THEME", raising=False)


class TestConfigDefaults:
    def test_defaults_when_no_file_and_no_env(self, tmp_path, monkeypatch):
        _clear_env(monkeypatch)
        with _patch_config_file(tmp_path / "config.toml"):
            config = load_config()
        assert config.max_history == 10
        assert config.shells == ("bash", "sh")
        assert config.no_color is False
        assert config.theme == "github-dark"


class TestConfigFile:
    def test_max_history_from_file(self, tmp_path, monkeypatch):
        _clear_env(monkeypatch)
        config_file = tmp_path / "config.toml"
        config_file.write_text("max_history = 5\n")
        with _patch_config_file(config_file):
            config = load_config()
        assert config.max_history == 5

    def test_shells_from_file(self, tmp_path, monkeypatch):
        _clear_env(monkeypatch)
        config_file = tmp_path / "config.toml"
        config_file.write_text('shells = ["zsh", "bash", "sh"]\n')
        with _patch_config_file(config_file):
            config = load_config()
        assert config.shells == ("zsh", "bash", "sh")

    def test_unknown_keys_ignored(self, tmp_path, monkeypatch):
        _clear_env(monkeypatch)
        config_file = tmp_path / "config.toml"
        config_file.write_text("max_history = 3\nunknown_key = true\n")
        with _patch_config_file(config_file):
            config = load_config()
        assert config.max_history == 3

    def test_malformed_toml_raises_system_exit(self, tmp_path, monkeypatch):
        _clear_env(monkeypatch)
        config_file = tmp_path / "config.toml"
        config_file.write_text("max_history = [not valid\n")
        with _patch_config_file(config_file):
            with pytest.raises(SystemExit, match="error reading"):
                load_config()


class TestEnvOverrides:
    def test_env_max_history_overrides_default(self, tmp_path, monkeypatch):
        _clear_env(monkeypatch)
        monkeypatch.setenv("KX_MAX_HISTORY", "20")
        with _patch_config_file(tmp_path / "config.toml"):
            config = load_config()
        assert config.max_history == 20

    def test_env_shells_overrides_default(self, tmp_path, monkeypatch):
        _clear_env(monkeypatch)
        monkeypatch.setenv("KX_SHELLS", "zsh,bash,sh")
        with _patch_config_file(tmp_path / "config.toml"):
            config = load_config()
        assert config.shells == ("zsh", "bash", "sh")

    def test_env_overrides_file(self, tmp_path, monkeypatch):
        _clear_env(monkeypatch)
        config_file = tmp_path / "config.toml"
        config_file.write_text("max_history = 5\n")
        monkeypatch.setenv("KX_MAX_HISTORY", "15")
        with _patch_config_file(config_file):
            config = load_config()
        assert config.max_history == 15

    def test_invalid_max_history_raises_system_exit(self, tmp_path, monkeypatch):
        _clear_env(monkeypatch)
        monkeypatch.setenv("KX_MAX_HISTORY", "notanumber")
        with _patch_config_file(tmp_path / "config.toml"):
            with pytest.raises(SystemExit, match="KX_MAX_HISTORY must be an integer"):
                load_config()


class TestNoColor:
    def test_no_color_from_file(self, tmp_path, monkeypatch):
        _clear_env(monkeypatch)
        config_file = tmp_path / "config.toml"
        config_file.write_text("no_color = true\n")
        with _patch_config_file(config_file):
            config = load_config()
        assert config.no_color is True

    @pytest.mark.parametrize(
        "value,expected", [("1", True), ("true", True), ("0", False), ("off", False)]
    )
    def test_no_color_from_env(self, tmp_path, monkeypatch, value, expected):
        _clear_env(monkeypatch)
        monkeypatch.setenv("KX_NO_COLOR", value)
        with _patch_config_file(tmp_path / "config.toml"):
            config = load_config()
        assert config.no_color is expected


class TestTheme:
    def test_theme_from_file(self, tmp_path, monkeypatch):
        _clear_env(monkeypatch)
        config_file = tmp_path / "config.toml"
        config_file.write_text('theme = "nord"\n')
        with _patch_config_file(config_file):
            config = load_config()
        assert config.theme == "nord"

    def test_env_theme_overrides_file(self, tmp_path, monkeypatch):
        _clear_env(monkeypatch)
        config_file = tmp_path / "config.toml"
        config_file.write_text('theme = "nord"\n')
        monkeypatch.setenv("KX_THEME", "dracula")
        with _patch_config_file(config_file):
            config = load_config()
        assert config.theme == "dracula"

    def test_unknown_theme_in_file_raises_system_exit(self, tmp_path, monkeypatch):
        _clear_env(monkeypatch)
        config_file = tmp_path / "config.toml"
        config_file.write_text('theme = "bogus"\n')
        with _patch_config_file(config_file):
            with pytest.raises(SystemExit, match="unknown theme"):
                load_config()

    def test_unknown_theme_in_env_raises_system_exit(self, tmp_path, monkeypatch):
        _clear_env(monkeypatch)
        monkeypatch.setenv("KX_THEME", "bogus")
        with _patch_config_file(tmp_path / "config.toml"):
            with pytest.raises(SystemExit, match="unknown theme"):
                load_config()


class TestSaveTheme:
    def test_creates_file_and_parent_dir(self, tmp_path, monkeypatch):
        _clear_env(monkeypatch)
        config_file = tmp_path / ".kx" / "config.toml"
        with _patch_config_file(config_file):
            save_theme("nord")
            config = load_config()
        assert config_file.read_text() == 'theme = "nord"\n'
        assert config.theme == "nord"

    def test_rewrites_existing_theme_preserving_other_content(
        self, tmp_path, monkeypatch
    ):
        _clear_env(monkeypatch)
        config_file = tmp_path / "config.toml"
        config_file.write_text('# my settings\nmax_history = 5\ntheme = "nord"\n')
        with _patch_config_file(config_file):
            save_theme("dracula")
            config = load_config()
        text = config_file.read_text()
        assert "# my settings" in text
        assert "max_history = 5" in text
        assert 'theme = "dracula"' in text
        assert "nord" not in text
        assert config.theme == "dracula"
        assert config.max_history == 5

    def test_appends_when_key_absent(self, tmp_path, monkeypatch):
        _clear_env(monkeypatch)
        config_file = tmp_path / "config.toml"
        config_file.write_text("max_history = 5")
        with _patch_config_file(config_file):
            save_theme("mono")
            config = load_config()
        assert config_file.read_text() == 'max_history = 5\ntheme = "mono"\n'
        assert config.theme == "mono"
