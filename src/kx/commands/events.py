from kx.events import EventRow, EventsServiceProtocol
from kx.kubectl import KubectlServiceProtocol
from kx.refresh import ensure_exists
from kx.state import StateServiceProtocol


class EventsCommand:
    def __init__(
        self,
        state: StateServiceProtocol,
        events: EventsServiceProtocol,
        kubectl: KubectlServiceProtocol,
    ):
        self.state = state
        self.events = events
        self.kubectl = kubectl

    def execute(self, index: int) -> list[EventRow]:
        name, namespace, kind = self.state.fields(index)
        all_events = self.events.get(namespace)
        filtered = self.events.filter(all_events, name, kind)

        if not filtered:
            # Deleted resources keep their events ~1h; only an empty result
            # warrants checking whether the target itself is gone.
            ensure_exists(self.kubectl, kind, name, namespace)
            return []

        rows = []
        for event in filtered:
            obj = event.involved_object
            timestamp = getattr(event, "last_timestamp", None) or getattr(
                event.metadata, "creation_timestamp", None
            )
            rows.append(
                EventRow(
                    type=event.type or "",
                    reason=event.reason or "",
                    kind=obj.kind,
                    message=event.message or "",
                    timestamp=timestamp,
                )
            )
        return rows
