"""Command-layer error handling: predictable failures must render a styled error
and exit 1, never surface a raw Python traceback."""

import re
from unittest.mock import MagicMock, patch

from kubernetes.client.exceptions import ApiException
from typer.testing import CliRunner

from kx.kinds import Kind
from kx.main import app
from kx.state import Query, State

runner = CliRunner()

_NO_STATE = "No state found. Run 'kx get <resource>' first."
_ANSI = re.compile(r"\x1b\[[0-9;]*m")


def _plain(text):
    """Strip Rich's ANSI colour codes so message assertions aren't split by them."""
    return _ANSI.sub("", text)


def _assert_clean_error(result, message):
    """The command exited 1 with a rendered message, not an uncaught exception."""
    assert result.exit_code == 1
    assert message in _plain(result.output)
    assert not isinstance(result.exception, (RuntimeError, ValueError))


class TestMissingStateRendersError:
    """Commands run before `kx get` must show the friendly hint, not a traceback.

    These commands historically called `_state.fields()` outside their try/except
    (or had no handler at all), so the RuntimeError escaped uncaught.
    """

    def _run(self, args):
        state = MagicMock()
        state.fields.side_effect = RuntimeError(_NO_STATE)
        with patch("kx.main._state", state):
            return runner.invoke(app, args)

    def test_describe(self):
        _assert_clean_error(self._run(["describe", "1"]), "No state found")

    def test_events(self):
        _assert_clean_error(self._run(["events", "1"]), "No state found")

    def test_logs(self):
        _assert_clean_error(self._run(["logs", "1"]), "No state found")

    def test_tree(self):
        _assert_clean_error(self._run(["tree", "1"]), "No state found")

    def test_port_forward(self):
        _assert_clean_error(
            self._run(["port-forward", "1", "8080:80"]), "No state found"
        )


class TestKubectlFailureRendersError:
    """A non-zero kubectl exit raises RuntimeError from KubectlService.run; these
    commands historically caught only ValueError, so it escaped as a traceback."""

    def _run(self, args):
        state = MagicMock()
        state.fields.return_value = ("nginx", "default", Kind.Deployment)
        kubectl = MagicMock()
        kubectl.run.side_effect = RuntimeError("the server rejected the request")
        with patch("kx.main._state", state), patch("kx.main._kubectl", kubectl):
            return runner.invoke(app, args)

    def test_scale(self):
        _assert_clean_error(
            self._run(["scale", "1", "3"]), "the server rejected the request"
        )

    def test_rollout(self):
        _assert_clean_error(
            self._run(["rollout", "restart", "1"]), "the server rejected the request"
        )


class TestControlFlowPreserved:
    """The decorator must not swallow typer.Exit / typer.Abort."""

    def test_delete_declined_confirmation_aborts(self):
        state = MagicMock()
        state.fields.return_value = ("nginx", "default", Kind.Deployment)
        kubectl = MagicMock()
        with patch("kx.main._state", state), patch("kx.main._kubectl", kubectl):
            result = runner.invoke(app, ["delete", "1"], input="n\n")
        assert result.exit_code != 0
        kubectl.run.assert_not_called()


class TestApiExceptionRendersError:
    """SDK-backed commands (tree, diagnostic) raise ApiException; non-404s must
    render styled, not traceback."""

    def test_tree_api_exception_renders_clean_error(self):
        state = MagicMock()
        state.fields.return_value = ("nginx", "default", Kind.Deployment)
        state.load.return_value = State(resources={"nginx": str(Kind.Deployment)})
        build = MagicMock(side_effect=ApiException(status=403, reason="Forbidden"))
        with patch("kx.main._state", state), patch("kx.main.build_tree", build):
            result = runner.invoke(app, ["tree", "1"])
        assert result.exit_code == 1
        assert "Kubernetes API error: 403 Forbidden" in _plain(result.output)
        assert not isinstance(result.exception, ApiException)


class TestStaleStateRefresh:
    def _run_stale_yaml(self, state):
        kubectl = MagicMock()
        kubectl.run.side_effect = [
            RuntimeError('Error from server (NotFound): pods "web-1" not found'),
            "NAME   READY\nweb-2  1/1",
        ]
        kubectl.current_namespace.return_value = "default"
        with patch("kx.main._state", state), patch("kx.main._kubectl", kubectl):
            return runner.invoke(app, ["yaml", "1"]), kubectl

    def _stale_state(self, query):
        state = MagicMock()
        state.fields.return_value = ("web-1", "default", Kind.Pod)
        state.load.return_value = State(
            resources={"web-1": str(Kind.Pod)}, namespace="default", query=query
        )
        return state

    def test_not_found_reruns_query_and_shows_fresh_list(self):
        state = self._stale_state(Query(resource="pods", args=[]))
        result, kubectl = self._run_stale_yaml(state)
        assert result.exit_code == 1
        output = _plain(result.output)
        assert "not found" in output
        assert "pick a new index" in output
        assert "web-2" in output
        kubectl.run.assert_any_call(["get", "pods"])
        saved = state.save.call_args[0][0]
        assert "web-2" in saved.resources

    def test_not_found_without_query_shows_hint(self):
        state = self._stale_state(None)
        result, kubectl = self._run_stale_yaml(state)
        assert result.exit_code == 1
        assert "kx get" in _plain(result.output)
        state.save.assert_not_called()

    def test_non_stale_error_does_not_refresh(self):
        state = self._stale_state(Query(resource="pods", args=[]))
        kubectl = MagicMock()
        kubectl.run.side_effect = RuntimeError("the server rejected the request")
        with patch("kx.main._state", state), patch("kx.main._kubectl", kubectl):
            result = runner.invoke(app, ["yaml", "1"])
        assert result.exit_code == 1
        assert "pick a new index" not in _plain(result.output)
        kubectl.run.assert_called_once()
