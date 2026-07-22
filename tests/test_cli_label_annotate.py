from unittest.mock import MagicMock, patch

from typer.testing import CliRunner

from kx.main import app
from kx.kinds import Kind

runner = CliRunner()


def _make_mocks(name="nginx", namespace="default", kind=str(Kind.Pod)):
    state = MagicMock()
    state.fields.return_value = (name, namespace, kind)
    kubectl = MagicMock()
    return state, kubectl


class TestLabelCommandWiring:
    def test_set_label(self):
        state, kubectl = _make_mocks()
        kubectl.run.side_effect = ['{"metadata": {"labels": {}}}', ""]
        with patch("kx.main._kubectl", kubectl), patch("kx.main._state", state):
            result = runner.invoke(app, ["label", "1", "env=prod"])
        assert result.exit_code == 0
        assert "Labeled Pod/nginx" in result.output

    def test_malformed_pair_errors(self):
        state, kubectl = _make_mocks()
        with patch("kx.main._kubectl", kubectl), patch("kx.main._state", state):
            result = runner.invoke(app, ["label", "1", "env"])
        assert result.exit_code == 1
        assert "env" in result.output

    def test_remove_label(self):
        state, kubectl = _make_mocks()
        kubectl.run.side_effect = ['{"metadata": {"labels": {"env": "prod"}}}', ""]
        with patch("kx.main._kubectl", kubectl), patch("kx.main._state", state):
            result = runner.invoke(app, ["label", "1", "--remove", "env"])
        assert result.exit_code == 0
        assert "removed 1" in result.output

    def test_empty_invocation_errors(self):
        state, kubectl = _make_mocks()
        with patch("kx.main._kubectl", kubectl), patch("kx.main._state", state):
            result = runner.invoke(app, ["label", "1"])
        assert result.exit_code == 1
        assert "nothing to set or remove" in result.output


class TestAnnotateCommandWiring:
    def test_set_annotation(self):
        state, kubectl = _make_mocks()
        kubectl.run.side_effect = ['{"metadata": {"annotations": {}}}', ""]
        with patch("kx.main._kubectl", kubectl), patch("kx.main._state", state):
            result = runner.invoke(app, ["annotate", "1", "note=x"])
        assert result.exit_code == 0
        assert "Annotated Pod/nginx" in result.output
