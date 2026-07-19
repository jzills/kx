from kx.events import EventsServiceProtocol
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

    def execute(self, index: int) -> str:
        name, namespace, kind = self.state.fields(index)
        all_events = self.events.get(namespace)
        filtered = self.events.filter(all_events, name, kind)

        if not filtered:
            # Deleted resources keep their events ~1h; only an empty result
            # warrants checking whether the target itself is gone.
            ensure_exists(self.kubectl, kind, name, namespace)
            return "No events found"

        output = []
        for event in filtered:
            obj = event.involved_object
            output.append(
                f"{event.type:8} {event.reason:30} "
                f"{obj.kind:10} {event.metadata.creation_timestamp} "
                f"{event.message}"
            )
        return "\n".join(output)
