"""Behavior of the ownership-tree builders in kx.graph, with the kubernetes
client mocked. These pin structure + indexing so the de-duplication refactor
is provably behavior-preserving."""

import re
from contextlib import contextmanager
from types import SimpleNamespace as NS
from unittest.mock import MagicMock, patch

from kx.graph import build_indexed_tree, build_tree
from kx.kinds import Kind


# --- fake kubernetes objects ------------------------------------------------


def _meta(name, uid="", owners=()):
    return NS(name=name, uid=uid, owner_references=[NS(uid=o) for o in owners])


def _res(name, uid="", owner=None):
    """A resource with owner metadata (deployment, replicaset, job, ...)."""
    owners = (owner,) if owner else ()
    return NS(metadata=_meta(name, uid, owners))


def _pod(name, uid="", owner=None, containers=("app",)):
    owners = (owner,) if owner else ()
    return NS(
        metadata=_meta(name, uid, owners),
        spec=NS(containers=[NS(name=c) for c in containers]),
    )


def _svc(name, selector):
    return NS(metadata=_meta(name), spec=NS(selector=selector))


def _items(*objs):
    return NS(items=list(objs))


@contextmanager
def _mocked(apps=None, core=None, batch=None):
    client = MagicMock()
    client.AppsV1Api.return_value = apps or MagicMock()
    client.CoreV1Api.return_value = core or MagicMock()
    client.BatchV1Api.return_value = batch or MagicMock()
    with patch("kx.graph.client", client), patch("kx.graph.load_config"):
        yield


def _flatten(tree, depth=0):
    """(depth, plain-text label) for the tree, Rich markup stripped."""
    rows = [(depth, re.sub(r"\[[^\]]*\]", "", str(tree.label)).strip())]
    for child in tree.children:
        rows.extend(_flatten(child, depth + 1))
    return rows


# --- build_tree -------------------------------------------------------------


class TestBuildTree:
    def test_deployment_expands_replicaset_pods_and_containers(self):
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items(
            _pod("web-1", "p1", owner="rsU"),
            _pod("web-2", "p2", owner="rsU"),
        )
        apps = MagicMock()
        apps.read_namespaced_deployment.return_value = _res("web", "deployU")
        apps.list_namespaced_replica_set.return_value = _items(
            _res("web-abc", "rsU", owner="deployU")
        )
        with _mocked(apps=apps, core=core):
            tree = build_tree(Kind.Deployment, "web", "default")
        assert _flatten(tree) == [
            (0, "Deployment/web"),
            (1, "rs/web-abc"),
            (2, "pod/web-1"),
            (3, "container: app"),
            (2, "pod/web-2"),
            (3, "container: app"),
        ]

    def test_statefulset_expands_owned_pods(self):
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items(_pod("db-0", "p0", owner="stsU"))
        apps = MagicMock()
        apps.read_namespaced_stateful_set.return_value = _res("db", "stsU")
        with _mocked(apps=apps, core=core):
            tree = build_tree(Kind.StatefulSet, "db", "default")
        assert _flatten(tree) == [
            (0, "StatefulSet/db"),
            (1, "pod/db-0"),
            (2, "container: app"),
        ]

    def test_replicaset_root_expands_owned_pods(self):
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items(_pod("rs-1", "p1", owner="rsU"))
        apps = MagicMock()
        apps.read_namespaced_replica_set.return_value = _res("web-abc", "rsU")
        with _mocked(apps=apps, core=core):
            tree = build_tree(Kind.ReplicaSet, "web-abc", "default")
        assert _flatten(tree) == [
            (0, "ReplicaSet/web-abc"),
            (1, "pod/rs-1"),
            (2, "container: app"),
        ]

    def test_daemonset_root_expands_owned_pods(self):
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items(_pod("ds-1", "p1", owner="dsU"))
        apps = MagicMock()
        apps.read_namespaced_daemon_set.return_value = _res("agent", "dsU")
        with _mocked(apps=apps, core=core):
            tree = build_tree(Kind.DaemonSet, "agent", "default")
        assert _flatten(tree) == [
            (0, "DaemonSet/agent"),
            (1, "pod/ds-1"),
            (2, "container: app"),
        ]

    def test_pod_shows_containers_directly(self):
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items()
        core.read_namespaced_pod.return_value = _pod(
            "solo", containers=("app", "sidecar")
        )
        with _mocked(core=core):
            tree = build_tree(Kind.Pod, "solo", "default")
        assert _flatten(tree) == [
            (0, "Pod/solo"),
            (1, "container: app"),
            (1, "container: sidecar"),
        ]

    def test_service_with_selector_lists_matching_pods(self):
        core = MagicMock()
        core.read_namespaced_service.return_value = _svc("api", {"app": "api"})
        core.list_namespaced_pod.return_value = _items(_pod("api-xyz"))
        with _mocked(core=core):
            tree = build_tree(Kind.Service, "api", "default")
        assert _flatten(tree) == [
            (0, "Service/api"),
            (1, "pod/api-xyz"),
            (2, "container: app"),
        ]

    def test_service_without_selector(self):
        core = MagicMock()
        core.read_namespaced_service.return_value = _svc("api", None)
        core.list_namespaced_pod.return_value = _items()
        with _mocked(core=core):
            tree = build_tree(Kind.Service, "api", "default")
        assert _flatten(tree) == [(0, "Service/api"), (1, "(no selector)")]

    def test_service_with_selector_but_no_pods(self):
        core = MagicMock()
        core.read_namespaced_service.return_value = _svc("api", {"app": "api"})
        core.list_namespaced_pod.return_value = _items()
        with _mocked(core=core):
            tree = build_tree(Kind.Service, "api", "default")
        assert _flatten(tree) == [(0, "Service/api"), (1, "(no matching pods)")]

    def test_unknown_kind_has_no_graph(self):
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items()
        with _mocked(core=core):
            tree = build_tree(Kind.ConfigMap, "cm", "default")
        assert _flatten(tree) == [
            (0, "ConfigMap/cm"),
            (1, "(no ownership graph for ConfigMap)"),
        ]


# --- build_indexed_tree -----------------------------------------------------


class TestBuildIndexedTree:
    def test_deployment_assigns_sequential_indexes_and_records_resources(self):
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items(
            _pod("web-1", "p1", owner="rsU"),
            _pod("web-2", "p2", owner="rsU"),
        )
        apps = MagicMock()
        apps.read_namespaced_deployment.return_value = _res("web", "deployU")
        apps.list_namespaced_replica_set.return_value = _items(
            _res("web-abc", "rsU", owner="deployU")
        )
        with _mocked(apps=apps, core=core):
            tree, resources = build_indexed_tree(Kind.Deployment, "web", "default")
        assert _flatten(tree) == [
            (0, "1 Deployment/web"),
            (1, "2 rs/web-abc"),
            (2, "3 pod/web-1"),
            (3, "container: app"),
            (2, "4 pod/web-2"),
            (3, "container: app"),
        ]
        assert resources == [
            ("web", Kind.Deployment),
            ("web-abc", Kind.ReplicaSet),
            ("web-1", Kind.Pod),
            ("web-2", Kind.Pod),
        ]

    def test_cronjob_records_jobs_and_pods(self):
        core = MagicMock()
        core.list_namespaced_pod.return_value = _items(
            _pod("cj-1-a", "pa", owner="jobU")
        )
        batch = MagicMock()
        batch.read_namespaced_cron_job.return_value = _res("cj", "cjU")
        batch.list_namespaced_job.return_value = _items(
            _res("cj-1", "jobU", owner="cjU")
        )
        with _mocked(core=core, batch=batch):
            tree, resources = build_indexed_tree(Kind.CronJob, "cj", "default")
        assert resources == [
            ("cj", Kind.CronJob),
            ("cj-1", Kind.Job),
            ("cj-1-a", Kind.Pod),
        ]
        assert _flatten(tree) == [
            (0, "1 CronJob/cj"),
            (1, "2 job/cj-1"),
            (2, "3 pod/cj-1-a"),
            (3, "container: app"),
        ]
