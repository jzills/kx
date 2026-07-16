import pytest
from kx.index import _parse_output, IndexService, resolve_index
from kx.state import State


# Realistic kubectl output — column spans are load-bearing; keep alignment exact.
PODS_OUTPUT = (
    "NAME             READY   STATUS    RESTARTS   AGE\n"
    "nginx-abc-xyz    1/1     Running   0          5d\n"
    "redis-def-uvw    1/1     Running   0          3d"
)

SINGLE_ROW_OUTPUT = (
    "NAME             READY   STATUS    RESTARTS   AGE\n"
    "only-pod-abc     1/1     Running   0          1d"
)


class TestParseOutput:
    def test_standard_output_returns_headers(self):
        headers, _, _ = _parse_output(PODS_OUTPUT)
        assert headers == ["NAME", "READY", "STATUS", "RESTARTS", "AGE"]

    def test_standard_output_returns_rows(self):
        _, rows, _ = _parse_output(PODS_OUTPUT)
        assert len(rows) == 2

    def test_standard_output_name_idx_is_zero(self):
        _, _, name_idx = _parse_output(PODS_OUTPUT)
        assert name_idx == 0

    def test_standard_output_row_values(self):
        _, rows, _ = _parse_output(PODS_OUTPUT)
        assert rows[0][0] == "nginx-abc-xyz"
        assert rows[1][0] == "redis-def-uvw"

    def test_empty_string(self):
        headers, rows, name_idx = _parse_output("")
        assert headers == []
        assert rows == []
        assert name_idx == 0

    def test_header_only_returns_empty_rows(self):
        headers, rows, _ = _parse_output(
            "NAME             READY   STATUS    RESTARTS   AGE"
        )
        assert headers == ["NAME", "READY", "STATUS", "RESTARTS", "AGE"]
        assert rows == []


class TestIndexServiceAdd:
    def setup_method(self):
        self.svc = IndexService()

    def test_add_prepends_x_column(self):
        output, _ = self.svc.add(PODS_OUTPUT)
        first_line = output.splitlines()[0]
        assert first_line.startswith("X")
        assert "NAME" in first_line

    def test_add_returns_correct_names(self):
        _, names = self.svc.add(PODS_OUTPUT)
        assert names == ["nginx-abc-xyz", "redis-def-uvw"]

    def test_add_indexes_are_one_based(self):
        output, _ = self.svc.add(PODS_OUTPUT)
        lines = output.splitlines()
        assert lines[1].startswith("1")
        assert lines[2].startswith("2")

    def test_add_empty_output_returns_original(self):
        output, names = self.svc.add("")
        assert output == ""
        assert names == []

    def test_add_json_output_returns_raw(self):
        json_output = '{\n  "items": []\n}'
        result_output, names = self.svc.add(json_output)
        assert result_output == json_output
        assert names == []

    def test_add_yaml_output_returns_raw(self):
        yaml_output = "apiVersion: v1\nkind: List\nitems: []"
        result_output, names = self.svc.add(yaml_output)
        assert result_output == yaml_output
        assert names == []

    def test_add_single_row(self):
        output, names = self.svc.add(SINGLE_ROW_OUTPUT)
        assert names == ["only-pod-abc"]
        lines = output.splitlines()
        assert lines[1].startswith("1")


class TestIndexServiceFilter:
    def setup_method(self):
        self.svc = IndexService()

    def test_filter_matching_term(self):
        result = self.svc.filter(PODS_OUTPUT, "nginx")
        assert "nginx-abc-xyz" in result
        assert "redis-def-uvw" not in result

    def test_filter_case_insensitive(self):
        result = self.svc.filter(PODS_OUTPUT, "NGINX")
        assert "nginx-abc-xyz" in result

    def test_filter_no_match_returns_header_only(self):
        result = self.svc.filter(PODS_OUTPUT, "notfound")
        lines = result.splitlines()
        assert len(lines) == 1
        assert "NAME" in lines[0]

    def test_filter_multiple_matches(self):
        output = (
            "NAME             READY   STATUS    RESTARTS   AGE\n"
            "app-v1-abc       1/1     Running   0          1d\n"
            "app-v2-def       1/1     Running   0          2d\n"
            "other-xyz        1/1     Running   0          3d"
        )
        result = self.svc.filter(output, "app")
        assert "app-v1-abc" in result
        assert "app-v2-def" in result
        assert "other-xyz" not in result

    def test_filter_preserves_header(self):
        result = self.svc.filter(PODS_OUTPUT, "nginx")
        assert result.splitlines()[0].startswith("NAME")


class TestResolveIndex:
    def test_valid_index_1(self):
        state = State(resources={"nginx": "Pod", "redis": "Pod"})
        assert resolve_index(state, 1) == "nginx"

    def test_valid_index_last(self):
        state = State(resources={"nginx": "Pod", "redis": "Pod"})
        assert resolve_index(state, 2) == "redis"

    def test_index_zero_raises(self):
        state = State(resources={"nginx": "Pod"})
        with pytest.raises(ValueError, match="out of range"):
            resolve_index(state, 0)

    def test_index_beyond_length_raises(self):
        state = State(resources={"nginx": "Pod"})
        with pytest.raises(ValueError, match="has 1 item "):
            resolve_index(state, 2)

    def test_index_negative_raises(self):
        state = State(resources={"nginx": "Pod"})
        with pytest.raises(ValueError, match="Index -1"):
            resolve_index(state, -1)
