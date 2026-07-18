from typer.testing import CliRunner

from kx.main import app

runner = CliRunner()


def test_version_flag():
    result = runner.invoke(app, ["--version"])
    assert result.exit_code == 0
    assert result.output.strip().startswith("kx ")


def test_version_short_flag():
    result = runner.invoke(app, ["-v"])
    assert result.exit_code == 0
    assert result.output.strip().startswith("kx ")


def test_help_banner_shows_version():
    result = runner.invoke(app, ["--help"])
    assert result.exit_code == 0
    assert "kubectl, indexed · v" in result.output


def test_help_groups_commands_into_sections():
    result = runner.invoke(app, ["--help"])
    assert result.exit_code == 0
    for section in ("Resources", "History", "Configuration", "Options"):
        assert section in result.output
    assert "theme" in result.output
    # History commands render after the Resources block
    assert result.output.index("get") < result.output.index("History")


def test_help_has_no_unmapped_commands():
    result = runner.invoke(app, ["--help"])
    assert "Other" not in result.output


def test_help_lists_version_option():
    result = runner.invoke(app, ["--help"])
    assert "--version" in result.output


def test_command_help_shows_examples():
    result = runner.invoke(app, ["get", "--help"])
    assert "Examples" in result.output
    assert "kx get pods" in result.output


def test_command_help_shows_aliases():
    result = runner.invoke(app, ["diagnostic", "--help"])
    assert "Aliases" in result.output
    assert "kx diag" in result.output


def test_theme_command_help_shows_examples():
    result = runner.invoke(app, ["theme", "--help"])
    assert "kx theme nord" in result.output
