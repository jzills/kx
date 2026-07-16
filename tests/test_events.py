from types import SimpleNamespace as NS
from unittest.mock import MagicMock, patch

import pytest

from kx.commands.events import EventsCommand
from kx.events import EventsService
from kx.kinds import Kind
from kx.refresh import StaleResourceError


def _event(type_, reason, kind, ts, message, name="web"):
    return NS(
        type=type_,
        reason=reason,
        message=message,
        involved_object=NS(kind=kind, name=name),
        metadata=NS(creation_timestamp=ts),
    )


class TestEventsCommand:
    def test_no_events_returns_placeholder(self):
        state = MagicMock()
        state.fields.return_value = ("web", "default", Kind.Pod)
        events = MagicMock()
        events.get.return_value = []
        events.filter.return_value = []

        kubectl = MagicMock()
        kubectl.probe.return_value = 0
        result = EventsCommand(state=state, events=events, kubectl=kubectl).execute(1)

        assert result == "No events found"
        events.get.assert_called_once_with("default")
        events.filter.assert_called_once_with([], "web", Kind.Pod)

    def test_formats_each_event(self):
        state = MagicMock()
        state.fields.return_value = ("web", "default", Kind.Pod)
        ev = _event(
            "Normal", "Scheduled", "Pod", "2024-01-01T00:00:00Z", "Assigned to node-1"
        )
        events = MagicMock()
        events.get.return_value = [ev]
        events.filter.return_value = [ev]

        result = EventsCommand(state=state, events=events, kubectl=MagicMock()).execute(
            1
        )

        assert "Normal" in result
        assert "Scheduled" in result
        assert "Pod" in result
        assert "Assigned to node-1" in result


class TestEventsStaleDetection:
    def _command(self, events_list, probe_rc):
        state = MagicMock()
        state.fields.return_value = ("web-1", "default", "Pod")
        events_svc = MagicMock()
        events_svc.get.return_value = events_list
        events_svc.filter.return_value = events_list
        kubectl = MagicMock()
        kubectl.probe.return_value = probe_rc
        cmd = EventsCommand(state=state, events=events_svc, kubectl=kubectl)
        return cmd, kubectl

    def test_no_events_and_missing_resource_raises_stale(self):
        cmd, kubectl = self._command([], probe_rc=1)
        with pytest.raises(StaleResourceError, match="Pod/web-1"):
            cmd.execute(1)
        kubectl.probe.assert_called_once_with(["get", "Pod", "web-1", "-n", "default"])

    def test_no_events_and_live_resource_returns_no_events(self):
        cmd, _ = self._command([], probe_rc=0)
        assert cmd.execute(1) == "No events found"

    def test_matched_events_skip_probe(self):
        event = _event(
            "Warning",
            "Killing",
            "Pod",
            "2024-01-01T00:00:00Z",
            "Stopping",
            name="web-1",
        )
        cmd, kubectl = self._command([event], probe_rc=1)
        cmd.execute(1)
        kubectl.probe.assert_not_called()


class TestEventsService:
    def test_filter_matches_name_and_kind(self):
        e1 = NS(involved_object=NS(name="web", kind="Pod"))
        e2 = NS(involved_object=NS(name="db", kind="Pod"))
        e3 = NS(involved_object=NS(name="web", kind="Service"))

        assert EventsService().filter([e1, e2, e3], "web", "Pod") == [e1]

    def test_get_queries_the_namespace(self):
        api = MagicMock()
        api.list_namespaced_event.return_value = NS(items=["e1", "e2"])
        client = MagicMock()
        client.CoreV1Api.return_value = api

        with patch("kx.events.client", client), patch("kx.events.load_config"):
            result = EventsService().get("kube-system")

        assert result == ["e1", "e2"]
        api.list_namespaced_event.assert_called_once_with(namespace="kube-system")
