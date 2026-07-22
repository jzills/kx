import pytest

from kx.scanner import build_engine_argv


class TestBuildEngineArgv:
    def test_scout_argv(self):
        assert build_engine_argv("scout", "nginx:1.25") == [
            "docker",
            "scout",
            "cves",
            "nginx:1.25",
        ]

    def test_appends_extra_args(self):
        assert build_engine_argv(
            "scout", "nginx:1.25", ["--only-severity", "critical"]
        ) == [
            "docker",
            "scout",
            "cves",
            "nginx:1.25",
            "--only-severity",
            "critical",
        ]

    def test_none_extra_args(self):
        assert build_engine_argv("scout", "redis:7", None) == [
            "docker",
            "scout",
            "cves",
            "redis:7",
        ]

    def test_unknown_engine_raises(self):
        with pytest.raises(ValueError, match="unknown engine 'bogus'"):
            build_engine_argv("bogus", "nginx:1.25")

    def test_unknown_engine_lists_available(self):
        with pytest.raises(ValueError, match="scout"):
            build_engine_argv("bogus", "nginx:1.25")
