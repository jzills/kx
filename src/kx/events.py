from dataclasses import dataclass
from datetime import datetime
from typing import Protocol

from kubernetes import client

from kx.k8s import load_config


@dataclass
class EventRow:
    """One event, flattened for rendering. `timestamp` is the event's last-seen
    time (falling back to creation) as a datetime, or None if unavailable."""

    type: str
    reason: str
    kind: str
    message: str
    timestamp: datetime | None = None


class EventsServiceProtocol(Protocol):
    def get(self, namespace: str) -> list: ...
    def filter(self, events: list, name: str, kind: str) -> list: ...


class EventsService:
    def get(self, namespace: str) -> list:
        load_config()
        v1 = client.CoreV1Api()
        return v1.list_namespaced_event(namespace=namespace).items

    def filter(self, events: list, name: str, kind: str) -> list:
        return [
            event
            for event in events
            if event.involved_object.name == name and event.involved_object.kind == kind
        ]
