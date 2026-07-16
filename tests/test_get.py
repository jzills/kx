from unittest.mock import MagicMock
from kx.commands.get import GetCommand, _extract_namespace
from kx.kinds import Kind
from kx.state import Query


def _make_command(kubectl_output="NAME\nnginx"):
    kubectl = MagicMock()
    kubectl.run.return_value = kubectl_output
    kubectl.current_namespace.return_value = "default"
    state = MagicMock()
    index = MagicMock()
    index.add.return_value = ("1  nginx", ["nginx"])
    return GetCommand(kubectl=kubectl, state=state, index=index), state, kubectl


class TestGetCommandExtraArgs:
    def test_extra_args_passed_through(self):
        cmd, _, kubectl = _make_command()
        cmd.execute("pods", extra_args=["-o", "wide"])
        kubectl.run.assert_called_once_with(["get", "pods", "-o", "wide"])

    def test_multiple_extra_args(self):
        cmd, _, kubectl = _make_command()
        cmd.execute("pods", extra_args=["--field-selector", "status.phase=Running"])
        kubectl.run.assert_called_once_with(
            ["get", "pods", "--field-selector", "status.phase=Running"]
        )

    def test_no_extra_args_passes_nothing(self):
        cmd, _, kubectl = _make_command()
        cmd.execute("pods")
        kubectl.run.assert_called_once_with(["get", "pods"])

    def test_sort_by_passed_through(self):
        cmd, _, kubectl = _make_command()
        cmd.execute("pods", extra_args=["--sort-by=.metadata.name"])
        kubectl.run.assert_called_once_with(["get", "pods", "--sort-by=.metadata.name"])


class TestGetCommandNonTabularOutput:
    def test_json_output_does_not_save_state(self):
        cmd, state, kubectl = _make_command()
        cmd.index.add.return_value = ('{\n  "items": []\n}', [])
        kubectl.run.return_value = '{\n  "items": []\n}'
        cmd.execute("pods", extra_args=["-o", "json"])
        state.save.assert_not_called()


class TestGetCommandNamespaceArgs:
    def test_all_namespaces_short_flag_skips_state(self):
        cmd, state, kubectl = _make_command()
        cmd.execute("pods", extra_args=["-A"])
        kubectl.run.assert_called_once_with(["get", "pods", "-A"])
        state.save.assert_not_called()

    def test_all_namespaces_long_flag_skips_state(self):
        cmd, state, kubectl = _make_command()
        cmd.execute("pods", extra_args=["--all-namespaces"])
        kubectl.run.assert_called_once_with(["get", "pods", "--all-namespaces"])
        state.save.assert_not_called()

    def test_explicit_namespace_short_used_for_state(self):
        cmd, state, kubectl = _make_command()
        cmd.execute("pods", extra_args=["-n", "kube-system"])
        kubectl.run.assert_called_once_with(["get", "pods", "-n", "kube-system"])
        saved = state.save.call_args[0][0]
        assert saved.namespace == "kube-system"

    def test_explicit_namespace_long_equals_used_for_state(self):
        cmd, state, kubectl = _make_command()
        cmd.execute("pods", extra_args=["--namespace=kube-system"])
        kubectl.run.assert_called_once_with(["get", "pods", "--namespace=kube-system"])
        saved = state.save.call_args[0][0]
        assert saved.namespace == "kube-system"

    def test_explicit_namespace_long_space_used_for_state(self):
        cmd, state, kubectl = _make_command()
        cmd.execute("pods", extra_args=["--namespace", "kube-system"])
        kubectl.run.assert_called_once_with(
            ["get", "pods", "--namespace", "kube-system"]
        )
        saved = state.save.call_args[0][0]
        assert saved.namespace == "kube-system"

    def test_no_namespace_falls_back_to_current(self):
        cmd, state, kubectl = _make_command()
        cmd.execute("pods")
        saved = state.save.call_args[0][0]
        assert saved.namespace == "default"


class TestExtractNamespace:
    def test_short_flag(self):
        assert _extract_namespace(["-n", "kube-system"]) == "kube-system"

    def test_long_flag_space(self):
        assert _extract_namespace(["--namespace", "kube-system"]) == "kube-system"

    def test_long_flag_equals(self):
        assert _extract_namespace(["--namespace=kube-system"]) == "kube-system"

    def test_no_namespace_returns_none(self):
        assert _extract_namespace(["--sort-by=.metadata.name"]) is None

    def test_empty_args(self):
        assert _extract_namespace([]) is None

    def test_short_flag_at_end_ignored(self):
        assert _extract_namespace(["-n"]) is None


class TestGetCommandRecordsQuery:
    def test_query_saved_with_state(self):
        cmd, state, _ = _make_command()
        cmd.execute("pods", filter_term="ngi", extra_args=["-n", "staging"])
        saved = state.save.call_args[0][0]
        assert saved.query == Query(
            resource="pods", args=["-n", "staging"], match="ngi"
        )

    def test_query_defaults(self):
        cmd, state, _ = _make_command()
        cmd.execute("pods")
        saved = state.save.call_args[0][0]
        assert saved.query == Query(resource="pods", args=[], match=None)


class TestGetCommandNormalizesKind:
    def test_alias_normalized_to_canonical_kind(self):
        cmd, state, _ = _make_command()
        cmd.execute("po")
        saved = state.save.call_args[0][0]
        assert list(saved.resources.values()) == [str(Kind.Pod)]

    def test_plural_alias_normalized(self):
        cmd, state, _ = _make_command()
        cmd.execute("pods")
        saved = state.save.call_args[0][0]
        assert list(saved.resources.values()) == [str(Kind.Pod)]

    def test_deploy_alias_normalized(self):
        cmd, state, _ = _make_command()
        cmd.execute("deploy")
        saved = state.save.call_args[0][0]
        assert list(saved.resources.values()) == [str(Kind.Deployment)]

    def test_unknown_crd_passes_through(self):
        cmd, state, _ = _make_command()
        cmd.execute("mycrd")
        saved = state.save.call_args[0][0]
        assert list(saved.resources.values()) == ["mycrd"]
