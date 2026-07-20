from unittest.mock import MagicMock

from kx.commands.top import TopCommand
from kx.kinds import Kind
from kx.state import Query


def _make_command(kubectl_output="NAME\nnginx"):
    kubectl = MagicMock()
    kubectl.run.return_value = kubectl_output
    kubectl.current_namespace.return_value = "default"
    state = MagicMock()
    index = MagicMock()
    index.add.return_value = ("1  nginx", ["nginx"])
    return TopCommand(kubectl=kubectl, state=state, index=index), state, kubectl


class TestTopCommandExtraArgs:
    def test_extra_args_passed_through(self):
        cmd, _, kubectl = _make_command()
        cmd.execute(extra_args=["--sort-by=cpu"])
        kubectl.run.assert_called_once_with(["top", "pods", "--sort-by=cpu"])

    def test_no_extra_args_passes_nothing(self):
        cmd, _, kubectl = _make_command()
        cmd.execute()
        kubectl.run.assert_called_once_with(["top", "pods"])

    def test_containers_flag_passed_through(self):
        cmd, _, kubectl = _make_command()
        cmd.execute(extra_args=["--containers"])
        kubectl.run.assert_called_once_with(["top", "pods", "--containers"])


class TestTopCommandNamespaceArgs:
    def test_all_namespaces_short_flag_skips_state(self):
        cmd, state, kubectl = _make_command()
        cmd.execute(extra_args=["-A"])
        kubectl.run.assert_called_once_with(["top", "pods", "-A"])
        state.save.assert_not_called()

    def test_all_namespaces_long_flag_skips_state(self):
        cmd, state, kubectl = _make_command()
        cmd.execute(extra_args=["--all-namespaces"])
        state.save.assert_not_called()

    def test_all_namespaces_output_is_not_indexed(self):
        cmd, state, kubectl = _make_command()
        kubectl.run.return_value = "NAMESPACE  NAME\ndefault    nginx"
        result = cmd.execute(extra_args=["-A"])
        assert result == "NAMESPACE  NAME\ndefault    nginx"
        cmd.index.add.assert_not_called()

    def test_explicit_namespace_used_for_state(self):
        cmd, state, kubectl = _make_command()
        cmd.execute(extra_args=["-n", "kube-system"])
        saved = state.save.call_args[0][0]
        assert saved.namespace == "kube-system"

    def test_no_namespace_falls_back_to_current(self):
        cmd, state, kubectl = _make_command()
        cmd.execute()
        saved = state.save.call_args[0][0]
        assert saved.namespace == "default"


class TestTopCommandNonTabularOutput:
    def test_no_rows_does_not_save_state(self):
        cmd, state, kubectl = _make_command()
        cmd.index.add.return_value = ("", [])
        cmd.execute()
        state.save.assert_not_called()


class TestTopCommandRecordsQuery:
    def test_query_saved_with_state(self):
        cmd, state, _ = _make_command()
        cmd.execute(filter_term="ngi", extra_args=["-n", "staging"])
        saved = state.save.call_args[0][0]
        assert saved.query == Query(
            resource="pods", args=["-n", "staging"], match="ngi"
        )

    def test_query_defaults(self):
        cmd, state, _ = _make_command()
        cmd.execute()
        saved = state.save.call_args[0][0]
        assert saved.query == Query(resource="pods", args=[], match=None)


class TestTopCommandSavesPodKind:
    def test_all_saved_resources_are_pods(self):
        cmd, state, _ = _make_command()
        cmd.execute()
        saved = state.save.call_args[0][0]
        assert list(saved.resources.values()) == [str(Kind.Pod)]


class TestTopCommandFilter:
    def test_filter_term_applied_before_indexing(self):
        cmd, state, kubectl = _make_command()
        cmd.index.filter.return_value = "NAME\nnginx"
        cmd.execute(filter_term="ngi")
        cmd.index.filter.assert_called_once_with("NAME\nnginx", "ngi")
