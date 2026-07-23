from dataclasses import dataclass, asdict
import json
import os
import tempfile
from pathlib import Path
from typing import Protocol

from kx.index import resolve_index
from kx.kinds import Kind


@dataclass
class Query:
    """The kx get invocation that produced a state entry, kept so a stale
    entry can be re-run. None for entries not created by kx get (tree --index,
    pre-existing state files)."""

    resource: str
    args: list[str]
    match: str | None = None


@dataclass
class State:
    resources: dict[str, Kind | str]  # name → kind, insertion-ordered
    namespace: str = "default"
    query: Query | None = None


def _state_from_dict(data: dict) -> State:
    query = data.get("query")
    return State(
        resources=data["resources"],
        namespace=data.get("namespace", "default"),
        query=Query(**query) if query else None,
    )


@dataclass
class StateHistory:
    states: list[State]
    cursor: int = 0


class StateServiceProtocol(Protocol):
    def save(self, state: State) -> None: ...
    def load(self) -> State: ...
    def load_history(self) -> StateHistory: ...
    def fields(self, index: int) -> tuple[str, str, str]: ...
    def navigate(self, delta: int) -> State: ...
    def navigate_to(self, position: int) -> State: ...
    def drop(self, position: int) -> StateHistory: ...


_STATE_FILE = Path.home() / ".kx" / "state.json"


class StateService:
    def __init__(self, max_history: int = 10) -> None:
        # Always keep at least one entry: max_history < 1 would otherwise make
        # the history slice below a no-op (list[-0:] is the whole list).
        self.max_history = max(1, max_history)

    def _load_history(self) -> StateHistory:
        if not _STATE_FILE.exists():
            raise RuntimeError("No state found. Run `kx get <resource>` first.")
        try:
            data = json.loads(_STATE_FILE.read_text())
            if "states" not in data:
                return StateHistory(states=[_state_from_dict(data)], cursor=0)
            states = [_state_from_dict(state_data) for state_data in data["states"]]
            return StateHistory(states=states, cursor=data["cursor"])
        except (json.JSONDecodeError, KeyError, TypeError) as e:
            # Corrupt or foreign JSON (partial write, hand-edit, old schema).
            # A `kx get` rebuilds state, so this is recoverable, not fatal.
            raise RuntimeError(
                f"State file at {_STATE_FILE} is unreadable ({e}). "
                f"Run `kx get <resource>` to rebuild it."
            ) from e

    def _save_history(self, history: StateHistory) -> None:
        _STATE_FILE.parent.mkdir(parents=True, exist_ok=True)
        data = {
            "states": [asdict(state) for state in history.states],
            "cursor": history.cursor,
        }
        # Write to a sibling temp file and atomically replace it in, so an
        # interrupted write can never leave a truncated/corrupt state.json.
        fd, tmp = tempfile.mkstemp(
            dir=_STATE_FILE.parent, prefix=".state-", suffix=".tmp"
        )
        try:
            with os.fdopen(fd, "w") as f:
                f.write(json.dumps(data))
            os.replace(tmp, _STATE_FILE)
        except BaseException:
            Path(tmp).unlink(missing_ok=True)
            raise

    def save(self, state: State) -> None:
        try:
            history = self._load_history()
            new_states = history.states[: history.cursor + 1] + [state]
        except RuntimeError:
            new_states = [state]
        new_states = new_states[-self.max_history :]
        self._save_history(StateHistory(states=new_states, cursor=len(new_states) - 1))

    def load(self) -> State:
        history = self._load_history()
        return history.states[history.cursor]

    def load_history(self) -> StateHistory:
        return self._load_history()

    def navigate(self, delta: int) -> State:
        history = self._load_history()
        history.cursor = max(0, min(len(history.states) - 1, history.cursor + delta))
        self._save_history(history)
        return history.states[history.cursor]

    def navigate_to(self, position: int) -> State:
        history = self._load_history()
        history.cursor = max(0, min(len(history.states) - 1, position - 1))
        self._save_history(history)
        return history.states[history.cursor]

    def drop(self, position: int) -> StateHistory:
        history = self._load_history()
        if len(history.states) == 1:
            raise RuntimeError("Cannot drop the only state entry.")
        index = max(0, min(len(history.states) - 1, position - 1))
        history.states.pop(index)
        if index < history.cursor:
            history.cursor -= 1
        else:
            history.cursor = min(history.cursor, len(history.states) - 1)
        self._save_history(history)
        return history

    def fields(self, index: int) -> tuple[str, str, str]:
        state = self.load()
        name = resolve_index(state, index)
        return name, state.namespace, state.resources[name]
