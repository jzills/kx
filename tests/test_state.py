import json
import pytest
from unittest.mock import MagicMock, patch
from kx.kinds import Kind
from kx.state import Query, State, StateHistory, StateService, previous_lists


def _patched(tmp_path):
    return patch("kx.state._STATE_FILE", tmp_path / "kx_state.json")


def _boom(*args, **kwargs):
    raise RuntimeError("boom")


class TestStateDataclass:
    def test_default_namespace_is_default(self):
        state = State(resources={"nginx": "Pod"})
        assert state.namespace == "default"

    def test_fields_are_set(self):
        state = State(resources={"app": "Deployment"}, namespace="staging")
        assert state.resources == {"app": "Deployment"}
        assert state.namespace == "staging"


class TestStateServiceSaveLoad:
    def test_round_trip(self, tmp_path):
        state = State(
            resources={"nginx": "Pod", "redis": "Pod"}, namespace="kube-system"
        )
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state)
            loaded = svc.load()
        assert loaded == state

    def test_round_trip_default_namespace(self, tmp_path):
        state = State(resources={"app": "Pod"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state)
            loaded = svc.load()
        assert loaded.namespace == "default"

    def test_load_missing_file_raises_runtime_error(self, tmp_path):
        with _patched(tmp_path):
            svc = StateService()
            with pytest.raises(RuntimeError, match="kx get"):
                svc.load()

    def test_save_writes_json_file(self, tmp_path):
        state_file = tmp_path / "kx_state.json"
        state = State(resources={"nginx": "Pod"})
        with patch("kx.state._STATE_FILE", state_file):
            StateService().save(state)
        assert state_file.exists()

    def test_load_returns_most_recent_after_multiple_saves(self, tmp_path):
        state1 = State(resources={"nginx": "Pod"}, namespace="default")
        state2 = State(resources={"myapp": "Deployment"}, namespace="prod")
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state1)
            svc.save(state2)
            loaded = svc.load()
        assert loaded == state2

    def test_legacy_format_loads_correctly(self, tmp_path):
        state_file = tmp_path / "kx_state.json"
        state_file.write_text(
            json.dumps({"resources": {"nginx": "Pod"}, "namespace": "staging"})
        )
        with patch("kx.state._STATE_FILE", state_file):
            loaded = StateService().load()
        assert loaded == State(resources={"nginx": "Pod"}, namespace="staging")


class TestStateServiceCorruptFile:
    def test_load_invalid_json_raises_runtime_error(self, tmp_path):
        state_file = tmp_path / "kx_state.json"
        state_file.write_text("this is not json{{{")
        with patch("kx.state._STATE_FILE", state_file):
            with pytest.raises(RuntimeError, match="unreadable"):
                StateService().load()

    def test_load_valid_json_missing_keys_raises_runtime_error(self, tmp_path):
        # Valid JSON but not a state document (missing 'resources').
        state_file = tmp_path / "kx_state.json"
        state_file.write_text("{}")
        with patch("kx.state._STATE_FILE", state_file):
            with pytest.raises(RuntimeError, match="unreadable"):
                StateService().load()

    def test_save_heals_corrupt_file(self, tmp_path):
        state_file = tmp_path / "kx_state.json"
        state_file.write_text("garbage")
        with patch("kx.state._STATE_FILE", state_file):
            svc = StateService()
            svc.save(State(resources={"nginx": "Pod"}))
            loaded = svc.load()
        assert loaded == State(resources={"nginx": "Pod"})


class TestStateServiceAtomicWrite:
    def test_save_failure_at_commit_preserves_existing_state(
        self, tmp_path, monkeypatch
    ):
        state_file = tmp_path / "kx_state.json"
        with patch("kx.state._STATE_FILE", state_file):
            svc = StateService()
            svc.save(State(resources={"good": "Pod"}, namespace="ns1"))
            original = state_file.read_text()
            # A crash at the atomic-commit step must leave the existing file
            # intact — the new state is written to a temp file and only swapped
            # in by the (here-failing) replace.
            monkeypatch.setattr("kx.state.os.replace", _boom)
            with pytest.raises(RuntimeError):
                svc.save(State(resources={"bad": "Pod"}))
            assert state_file.read_text() == original
        # The temp file must be cleaned up, not left orphaned.
        leftovers = [p.name for p in tmp_path.iterdir() if p.name != state_file.name]
        assert leftovers == []


class TestStateServiceHistory:
    def test_zero_max_history_keeps_at_least_one(self, tmp_path):
        with _patched(tmp_path):
            svc = StateService(max_history=0)
            for number in range(3):
                svc.save(State(resources={f"pod-{number}": "Pod"}))
            history = svc._load_history()
        assert len(history.states) == 1

    def test_history_preserves_previous_states(self, tmp_path):
        state1 = State(resources={"nginx": "Pod"})
        state2 = State(resources={"myapp": "Deployment"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state1)
            svc.save(state2)
            history = svc._load_history()
        assert len(history.states) == 2
        assert history.states[0] == state1
        assert history.states[1] == state2
        assert history.cursor == 1

    def test_history_capped_at_max(self, tmp_path):
        with _patched(tmp_path):
            svc = StateService()
            for number in range(12):
                svc.save(State(resources={f"pod-{number}": "Pod"}))
            history = svc._load_history()
        assert len(history.states) == 10

    def test_custom_max_history_respected(self, tmp_path):
        with _patched(tmp_path):
            svc = StateService(max_history=3)
            for number in range(5):
                svc.save(State(resources={f"pod-{number}": "Pod"}))
            history = svc._load_history()
        assert len(history.states) == 3

    def test_history_cap_drops_oldest(self, tmp_path):
        with _patched(tmp_path):
            svc = StateService()
            for number in range(11):
                svc.save(State(resources={f"pod-{number}": "Pod"}))
            history = svc._load_history()
        assert "pod-0" not in history.states[0].resources
        assert "pod-1" in history.states[0].resources

    def test_navigate_back_returns_previous_state(self, tmp_path):
        state1 = State(resources={"nginx": "Pod"})
        state2 = State(resources={"myapp": "Deployment"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state1)
            svc.save(state2)
            result = svc.navigate(-1)
        assert result == state1

    def test_navigate_forward_returns_newer_state(self, tmp_path):
        state1 = State(resources={"nginx": "Pod"})
        state2 = State(resources={"myapp": "Deployment"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state1)
            svc.save(state2)
            svc.navigate(-1)
            result = svc.navigate(+1)
        assert result == state2

    def test_navigate_back_clamps_at_oldest(self, tmp_path):
        state = State(resources={"nginx": "Pod"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state)
            result = svc.navigate(-1)
        assert result == state

    def test_navigate_forward_clamps_at_newest(self, tmp_path):
        state = State(resources={"nginx": "Pod"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state)
            result = svc.navigate(+1)
        assert result == state

    def test_new_save_after_back_truncates_forward_history(self, tmp_path):
        state1 = State(resources={"nginx": "Pod"})
        state2 = State(resources={"myapp": "Deployment"})
        state3 = State(resources={"svc": "Service"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state1)
            svc.save(state2)
            svc.navigate(-1)  # go back to state1
            svc.save(state3)  # should replace state2 in forward history
            history = svc._load_history()
        assert len(history.states) == 2
        assert history.states[0] == state1
        assert history.states[1] == state3

    def test_navigate_persists_cursor(self, tmp_path):
        state1 = State(resources={"nginx": "Pod"})
        state2 = State(resources={"myapp": "Deployment"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state1)
            svc.save(state2)
            svc.navigate(-1)
            loaded = svc.load()
        assert loaded == state1

    def test_navigate_to_jumps_to_position(self, tmp_path):
        state1 = State(resources={"nginx": "Pod"})
        state2 = State(resources={"myapp": "Deployment"})
        state3 = State(resources={"svc": "Service"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state1)
            svc.save(state2)
            svc.save(state3)
            result = svc.navigate_to(1)
        assert result == state1

    def test_navigate_to_middle_position(self, tmp_path):
        state1 = State(resources={"nginx": "Pod"})
        state2 = State(resources={"myapp": "Deployment"})
        state3 = State(resources={"svc": "Service"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state1)
            svc.save(state2)
            svc.save(state3)
            result = svc.navigate_to(2)
        assert result == state2

    def test_navigate_to_clamps_below_one(self, tmp_path):
        state1 = State(resources={"nginx": "Pod"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state1)
            result = svc.navigate_to(0)
        assert result == state1

    def test_navigate_to_clamps_above_max(self, tmp_path):
        state1 = State(resources={"nginx": "Pod"})
        state2 = State(resources={"myapp": "Deployment"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state1)
            svc.save(state2)
            result = svc.navigate_to(99)
        assert result == state2

    def test_navigate_to_persists_cursor(self, tmp_path):
        state1 = State(resources={"nginx": "Pod"})
        state2 = State(resources={"myapp": "Deployment"})
        state3 = State(resources={"svc": "Service"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state1)
            svc.save(state2)
            svc.save(state3)
            svc.navigate_to(2)
            loaded = svc.load()
        assert loaded == state2

    def test_load_history_returns_state_history(self, tmp_path):
        state1 = State(resources={"nginx": "Pod"})
        state2 = State(resources={"myapp": "Deployment"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state1)
            svc.save(state2)
            history = svc.load_history()
        assert len(history.states) == 2
        assert history.cursor == 1

    def test_drop_removes_entry(self, tmp_path):
        state1 = State(resources={"nginx": "Pod"})
        state2 = State(resources={"myapp": "Deployment"})
        state3 = State(resources={"svc": "Service"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state1)
            svc.save(state2)
            svc.save(state3)
            history = svc.drop(2)
        assert len(history.states) == 2
        assert state2 not in history.states

    def test_drop_before_cursor_decrements_cursor(self, tmp_path):
        state1 = State(resources={"nginx": "Pod"})
        state2 = State(resources={"myapp": "Deployment"})
        state3 = State(resources={"svc": "Service"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state1)
            svc.save(state2)
            svc.save(state3)
            history = svc.drop(1)
        assert history.cursor == 1
        assert history.states[history.cursor] == state3

    def test_drop_at_cursor_stays_on_next_entry(self, tmp_path):
        state1 = State(resources={"nginx": "Pod"})
        state2 = State(resources={"myapp": "Deployment"})
        state3 = State(resources={"svc": "Service"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state1)
            svc.save(state2)
            svc.save(state3)
            svc.navigate(-1)  # cursor → 1 (state2)
            history = svc.drop(2)
        assert history.states[history.cursor] == state3

    def test_drop_last_entry_at_cursor_moves_to_new_last(self, tmp_path):
        state1 = State(resources={"nginx": "Pod"})
        state2 = State(resources={"myapp": "Deployment"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state1)
            svc.save(state2)
            history = svc.drop(2)
        assert history.cursor == 0
        assert history.states[history.cursor] == state1

    def test_drop_after_cursor_leaves_cursor_unchanged(self, tmp_path):
        state1 = State(resources={"nginx": "Pod"})
        state2 = State(resources={"myapp": "Deployment"})
        state3 = State(resources={"svc": "Service"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state1)
            svc.save(state2)
            svc.save(state3)
            svc.navigate(-2)  # cursor → 0 (state1)
            history = svc.drop(3)
        assert history.cursor == 0
        assert history.states[history.cursor] == state1

    def test_drop_only_entry_raises(self, tmp_path):
        with _patched(tmp_path):
            svc = StateService()
            svc.save(State(resources={"nginx": "Pod"}))
            with pytest.raises(RuntimeError, match="only state"):
                svc.drop(1)

    def test_drop_clamps_out_of_bounds_position(self, tmp_path):
        state1 = State(resources={"nginx": "Pod"})
        state2 = State(resources={"myapp": "Deployment"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state1)
            svc.save(state2)
            history = svc.drop(99)
        assert len(history.states) == 1
        assert state2 not in history.states


class TestQueryPersistence:
    def test_query_round_trips(self, tmp_path):
        state = State(
            resources={"nginx": "Pod"},
            namespace="staging",
            query=Query(resource="pods", args=["-n", "staging"], match="ngi"),
        )
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state)
            loaded = svc.load()
        assert loaded == state
        assert loaded.query == Query(
            resource="pods", args=["-n", "staging"], match="ngi"
        )

    def test_state_without_query_defaults_to_none(self, tmp_path):
        state = State(resources={"nginx": "Pod"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state)
            loaded = svc.load()
        assert loaded.query is None

    def test_pre_query_state_file_loads_with_none(self, tmp_path):
        state_file = tmp_path / "kx_state.json"
        state_file.write_text(
            json.dumps(
                {
                    "states": [{"resources": {"nginx": "Pod"}, "namespace": "prod"}],
                    "cursor": 0,
                }
            )
        )
        with patch("kx.state._STATE_FILE", state_file):
            loaded = StateService().load()
        assert loaded == State(resources={"nginx": "Pod"}, namespace="prod")
        assert loaded.query is None

    def test_legacy_single_state_format_loads_with_none(self, tmp_path):
        state_file = tmp_path / "kx_state.json"
        state_file.write_text(
            json.dumps({"resources": {"nginx": "Pod"}, "namespace": "staging"})
        )
        with patch("kx.state._STATE_FILE", state_file):
            loaded = StateService().load()
        assert loaded.query is None


class TestStateServiceFields:
    def test_fields_index_1(self, tmp_path):
        state = State(
            resources={"nginx": "Pod", "redis": "Pod"}, namespace="kube-system"
        )
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state)
            name, namespace, kind = svc.fields(1)
        assert name == "nginx"
        assert namespace == "kube-system"
        assert kind == "Pod"

    def test_fields_index_2(self, tmp_path):
        state = State(resources={"nginx": "Pod", "redis": "Pod"}, namespace="default")
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state)
            name, _, _ = svc.fields(2)
        assert name == "redis"

    def test_fields_heterogeneous_kinds(self, tmp_path):
        state = State(
            resources={"my-rs": "ReplicaSet", "my-pod": "Pod"}, namespace="default"
        )
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state)
            _, _, kind1 = svc.fields(1)
            _, _, kind2 = svc.fields(2)
        assert kind1 == "ReplicaSet"
        assert kind2 == "Pod"

    def test_fields_invalid_index_raises(self, tmp_path):
        state = State(resources={"nginx": "Pod"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state)
            with pytest.raises(ValueError, match="out of range"):
                svc.fields(0)

    def test_fields_out_of_bounds_raises(self, tmp_path):
        state = State(resources={"nginx": "Pod"})
        with _patched(tmp_path):
            svc = StateService()
            svc.save(state)
            with pytest.raises(ValueError, match="out of range"):
                svc.fields(5)


class TestPreviousLists:
    def _service(self, states, cursor):
        service = MagicMock()
        service.load_history.return_value = StateHistory(states=states, cursor=cursor)
        return service

    def test_true_when_entry_one_step_back_lists_the_kind(self):
        service = self._service(
            [
                State(resources={"db": "Namespace"}),
                State(resources={"web-0": "Pod"}),
            ],
            cursor=1,
        )
        assert previous_lists(service, Kind.Namespace) is True

    def test_false_when_entry_one_step_back_lists_something_else(self):
        service = self._service(
            [
                State(resources={"api": "Deployment"}),
                State(resources={"web-0": "Pod"}),
            ],
            cursor=1,
        )
        assert previous_lists(service, Kind.Namespace) is False

    def test_false_at_the_start_of_history(self):
        service = self._service([State(resources={"db": "Namespace"})], cursor=0)
        assert previous_lists(service, Kind.Namespace) is False

    def test_false_when_history_is_unreadable(self):
        service = MagicMock()
        service.load_history.side_effect = RuntimeError("No state found.")
        assert previous_lists(service, Kind.Namespace) is False
