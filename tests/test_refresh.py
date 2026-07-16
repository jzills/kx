from unittest.mock import MagicMock

import pytest
from kubernetes.client.exceptions import ApiException

from kx.refresh import RefreshService, StaleResourceError, ensure_exists
from kx.state import Query, State


def _service(state=None):
    kubectl = MagicMock()
    kubectl.run.return_value = "NAME\nweb-2"
    index = MagicMock()
    index.add.return_value = ("X  NAME\n1  web-2", ["web-2"])
    state_svc = MagicMock()
    state_svc.load.return_value = state
    return RefreshService(state=state_svc, kubectl=kubectl, index=index), state_svc


class TestMatches:
    def _matches(self, error):
        svc, _ = _service()
        return svc.matches(error)

    def test_kubectl_not_found_matches(self):
        assert self._matches(
            RuntimeError('Error from server (NotFound): pods "web-1" not found')
        )

    def test_generic_runtime_error_does_not_match(self):
        assert not self._matches(RuntimeError("the server rejected the request"))

    def test_missing_state_error_does_not_match(self):
        assert not self._matches(
            RuntimeError("No state found. Run `kx get <resource>` first.")
        )

    def test_api_exception_404_matches(self):
        assert self._matches(ApiException(status=404, reason="Not Found"))

    def test_api_exception_403_does_not_match(self):
        assert not self._matches(ApiException(status=403, reason="Forbidden"))

    def test_stale_resource_error_matches(self):
        assert self._matches(StaleResourceError("Pod/web-1 no longer exists"))

    def test_value_error_does_not_match(self):
        assert not self._matches(ValueError("Index 3 is out of range"))


class TestRecover:
    def test_recover_reruns_saved_query(self):
        stale = State(
            resources={"web-1": "Pod"},
            namespace="staging",
            query=Query(resource="pods", args=["-n", "staging"], match=None),
        )
        svc, state_svc = _service(stale)
        result = svc.recover()
        assert result == ("X  NAME\n1  web-2", "pods", "staging")
        saved = state_svc.save.call_args[0][0]
        assert saved.resources == {"web-2": "Pod"}
        assert saved.query == Query(resource="pods", args=["-n", "staging"], match=None)

    def test_recover_without_query_returns_none(self):
        svc, state_svc = _service(State(resources={"web-1": "Pod"}))
        assert svc.recover() is None
        state_svc.save.assert_not_called()


class TestEnsureExists:
    def test_missing_resource_raises(self):
        kubectl = MagicMock()
        kubectl.probe.return_value = 1
        with pytest.raises(StaleResourceError, match="Pod/web-1 no longer exists"):
            ensure_exists(kubectl, "Pod", "web-1", "default")
        kubectl.probe.assert_called_once_with(["get", "Pod", "web-1", "-n", "default"])

    def test_live_resource_passes(self):
        kubectl = MagicMock()
        kubectl.probe.return_value = 0
        ensure_exists(kubectl, "Pod", "web-1", "default")
