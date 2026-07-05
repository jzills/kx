from kubernetes import client
from rich.tree import Tree

from kx.k8s import load_config
from kx.kinds import Kind

_COLOR_ROOT = "#3fb950"
_COLOR_MID = "#3fb950"
_COLOR_POD = "#e6edf3"
_COLOR_DIM = "#7d8590"


def build_tree(kind: str, name: str, namespace: str) -> Tree:
    root, _ = _build(kind, name, namespace, indexed=False)
    return root


def build_indexed_tree(
    kind: str, name: str, namespace: str
) -> tuple[Tree, list[tuple[str, str]]]:
    return _build(kind, name, namespace, indexed=True)


def _build(
    kind: str, name: str, namespace: str, indexed: bool
) -> tuple[Tree, list[tuple[str, str]]]:
    load_config()
    resources: list[tuple[str, str]] = [(name, kind)] if indexed else []
    label = f"[bold {_COLOR_ROOT}]{kind}/{name}[/bold {_COLOR_ROOT}]"
    if indexed:
        label = f"[{_COLOR_DIM}]1[/{_COLOR_DIM}] {label}"
    root = Tree(label)

    apps = client.AppsV1Api()
    core = client.CoreV1Api()
    batch = client.BatchV1Api()

    pods = core.list_namespaced_pod(namespace).items

    match kind:
        case Kind.Deployment:
            _tree_deployment(name, namespace, root, apps, pods, resources, indexed)
        case Kind.ReplicaSet:
            _tree_replica_set(name, namespace, root, apps, pods, resources, indexed)
        case Kind.StatefulSet:
            _tree_stateful_set(name, namespace, root, apps, pods, resources, indexed)
        case Kind.DaemonSet:
            _tree_daemon_set(name, namespace, root, apps, pods, resources, indexed)
        case Kind.CronJob:
            _tree_cron_job(name, namespace, root, batch, pods, resources, indexed)
        case Kind.Service:
            _tree_service(name, namespace, root, core, resources, indexed)
        case Kind.Pod:
            pod = core.read_namespaced_pod(name, namespace)
            _add_containers(pod, root)
        case _:
            root.add(f"[dim](no ownership graph for {kind})[/dim]")

    return root, resources


def _add_node(parent, resources, indexed, color, prefix, name, kind):
    """Add a labelled child node; when indexed, prepend its 1-based index and
    record `(name, kind)` in `resources`."""
    label = f"[{color}]{prefix}/{name}[/{color}]"
    if indexed:
        label = f"[{_COLOR_DIM}]{len(resources) + 1}[/{_COLOR_DIM}] {label}"
        resources.append((name, kind))
    return parent.add(label)


def _tree_deployment(name, namespace, node, apps, pods, resources, indexed):
    deploy = apps.read_namespaced_deployment(name, namespace)
    uid = deploy.metadata.uid
    replica_sets = [
        rs
        for rs in apps.list_namespaced_replica_set(namespace).items
        if _owned_by(rs, uid)
    ]
    for rs in replica_sets:
        rs_node = _add_node(
            node,
            resources,
            indexed,
            _COLOR_MID,
            "rs",
            rs.metadata.name,
            Kind.ReplicaSet,
        )
        _add_pods_for_owner(rs.metadata.uid, pods, rs_node, resources, indexed)


def _tree_replica_set(name, namespace, node, apps, pods, resources, indexed):
    rs = apps.read_namespaced_replica_set(name, namespace)
    _add_pods_for_owner(rs.metadata.uid, pods, node, resources, indexed)


def _tree_stateful_set(name, namespace, node, apps, pods, resources, indexed):
    sts = apps.read_namespaced_stateful_set(name, namespace)
    _add_pods_for_owner(sts.metadata.uid, pods, node, resources, indexed)


def _tree_daemon_set(name, namespace, node, apps, pods, resources, indexed):
    ds = apps.read_namespaced_daemon_set(name, namespace)
    _add_pods_for_owner(ds.metadata.uid, pods, node, resources, indexed)


def _tree_cron_job(name, namespace, node, batch, pods, resources, indexed):
    cj = batch.read_namespaced_cron_job(name, namespace)
    uid = cj.metadata.uid
    jobs = [
        job for job in batch.list_namespaced_job(namespace).items if _owned_by(job, uid)
    ]
    for job in jobs:
        job_node = _add_node(
            node, resources, indexed, _COLOR_MID, "job", job.metadata.name, Kind.Job
        )
        _add_pods_for_owner(job.metadata.uid, pods, job_node, resources, indexed)


def _tree_service(name, namespace, node, core, resources, indexed):
    svc = core.read_namespaced_service(name, namespace)
    selector = svc.spec.selector
    if not selector:
        node.add(f"[{_COLOR_DIM}](no selector)[/{_COLOR_DIM}]")
        return
    label_selector = ",".join(f"{k}={v}" for k, v in selector.items())
    pods = core.list_namespaced_pod(namespace, label_selector=label_selector).items
    if not pods:
        node.add(f"[{_COLOR_DIM}](no matching pods)[/{_COLOR_DIM}]")
        return
    for pod in pods:
        pod_node = _add_node(
            node, resources, indexed, _COLOR_POD, "pod", pod.metadata.name, Kind.Pod
        )
        _add_containers(pod, pod_node)


def _add_pods_for_owner(owner_uid, pods, parent_node, resources, indexed):
    owned = [pod for pod in pods if _owned_by(pod, owner_uid)]
    for pod in owned:
        pod_node = _add_node(
            parent_node,
            resources,
            indexed,
            _COLOR_POD,
            "pod",
            pod.metadata.name,
            Kind.Pod,
        )
        _add_containers(pod, pod_node)


def _add_containers(pod, parent_node):
    for container in pod.spec.containers:
        parent_node.add(f"[{_COLOR_DIM}]container: {container.name}[/{_COLOR_DIM}]")


def _owned_by(resource, uid: str) -> bool:
    refs = resource.metadata.owner_references or []
    return any(ref.uid == uid for ref in refs)
