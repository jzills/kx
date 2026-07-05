from types import SimpleNamespace as NS
from unittest.mock import MagicMock, patch

from kx.commands.events import EventsCommand
from kx.events import EventsService
from kx.kinds import Kind


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

        result = EventsCommand(state=state, events=events).execute(1)

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

        result = EventsCommand(state=state, events=events).execute(1)

        assert "Normal" in result
        assert "Scheduled" in result
        assert "Pod" in result
        assert "Assigned to node-1" in result


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
