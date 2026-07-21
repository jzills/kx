import json
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
    index.filter.side_effect = lambda output, term: output
    index.add.return_value = ("1  nginx", ["nginx"])
    return TopCommand(kubectl=kubectl, state=state, index=index), state, kubectl


_TOP_OUTPUT = "NAME     CPU(cores)   MEMORY(bytes)\nweb-1    50m          100Mi"


def _pods_json(*pods):
    return json.dumps({"items": list(pods)})


def _pod_item(name, containers):
    return {
        "metadata": {"name": name},
        "spec": {
            "containers": [
                {"name": c["name"], "resources": {"limits": c.get("limits", {})}}
                for c in containers
            ]
        },
    }


def _make_usage_command(top_output=_TOP_OUTPUT, pods_json=None):
    kubectl = MagicMock()
    kubectl.current_namespace.return_value = "default"

    def run(args):
        if args[:2] == ["top", "pods"]:
            return top_output
        if args[:2] == ["get", "pods"]:
            return pods_json if pods_json is not None else _pods_json()
        raise AssertionError(f"unexpected kubectl args: {args}")

    kubectl.run.side_effect = run
    state = MagicMock()
    index = MagicMock()
    index.add.side_effect = lambda output: (
        output,
        [line.split()[0] for line in output.splitlines()[1:] if line.strip()],
    )
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


class TestTopCommandUsagePercentages:
    def test_percentage_columns_added_by_default(self):
        cmd, state, kubectl = _make_usage_command(
            pods_json=_pods_json(
                _pod_item(
                    "web-1",
                    [{"name": "app", "limits": {"cpu": "100m", "memory": "200Mi"}}],
                )
            )
        )
        result = cmd.execute()
        assert "CPU%" in result
        assert "MEM%" in result
        assert "50%" in result  # 50m/100m cpu and 100Mi/200Mi memory both land at 50%

    def test_second_kubectl_call_scoped_to_namespace(self):
        cmd, state, kubectl = _make_usage_command()
        cmd.execute(extra_args=["-n", "kube-system"])
        kubectl.run.assert_any_call(["get", "pods", "-n", "kube-system", "-o", "json"])

    def test_no_limits_flag_skips_second_call(self):
        cmd, state, kubectl = _make_usage_command()
        cmd.execute(no_limits=True)
        assert kubectl.run.call_count == 1
        kubectl.run.assert_called_once_with(["top", "pods"])

    def test_containers_flag_skips_percentages(self):
        cmd, state, kubectl = _make_usage_command(
            top_output="POD  NAME  CPU(cores)  MEMORY(bytes)"
        )
        result = cmd.execute(extra_args=["--containers"])
        assert kubectl.run.call_count == 1
        assert "CPU%" not in result

    def test_all_namespaces_skips_percentages(self):
        cmd, state, kubectl = _make_usage_command(
            top_output="NAMESPACE  NAME  CPU(cores)  MEMORY(bytes)"
        )
        result = cmd.execute(extra_args=["-A"])
        assert kubectl.run.call_count == 1
        assert "CPU%" not in result

    def test_missing_limit_renders_dash(self):
        cmd, state, kubectl = _make_usage_command(
            pods_json=_pods_json(_pod_item("web-1", [{"name": "app", "limits": {}}]))
        )
        result = cmd.execute()
        lines = result.splitlines()
        data_line = [line for line in lines if "web-1" in line][0]
        assert "—" in data_line

    def test_partial_limit_dashes_only_missing_resource(self):
        cmd, state, kubectl = _make_usage_command(
            pods_json=_pods_json(
                _pod_item("web-1", [{"name": "app", "limits": {"cpu": "100m"}}])
            )
        )
        result = cmd.execute()
        data_line = [line for line in result.splitlines() if "web-1" in line][0]
        assert "50%" in data_line  # 50m/100m cpu
        assert "—" in data_line  # no memory limit

    def test_multi_container_limits_summed(self):
        cmd, state, kubectl = _make_usage_command(
            pods_json=_pods_json(
                _pod_item(
                    "web-1",
                    [
                        {"name": "app", "limits": {"cpu": "50m", "memory": "100Mi"}},
                        {
                            "name": "sidecar",
                            "limits": {"cpu": "50m", "memory": "100Mi"},
                        },
                    ],
                )
            )
        )
        result = cmd.execute()
        data_line = [line for line in result.splitlines() if "web-1" in line][0]
        assert "50%" in data_line  # 50m usage / (50m+50m) = 50%

    def test_pod_without_limits_entry_renders_dash(self):
        cmd, state, kubectl = _make_usage_command(pods_json=_pods_json())
        result = cmd.execute()
        data_line = [line for line in result.splitlines() if "web-1" in line][0]
        assert "—" in data_line


class TestTopCommandFilter:
    def test_filter_term_applied_before_indexing(self):
        cmd, state, kubectl = _make_command()
        cmd.index.filter.return_value = "NAME\nnginx"
        cmd.execute(filter_term="ngi")
        cmd.index.filter.assert_called_once_with("NAME\nnginx", "ngi")
