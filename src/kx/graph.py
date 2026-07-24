from kubernetes import client
from rich.tree import Tree

from kx.k8s import load_config
from kx.kinds import Kind


def build_tree(kind: str, name: str, namespace: str) -> Tree:
    root, _ = _build(kind, name, namespace, indexed=False)
    return root


def build_indexed_tree(
    kind: str, name: str, namespace: str
) -> tuple[Tree, list[tuple[str, str]]]:
    return _build(kind, name, namespace, indexed=True)


def build_namespace_tree(namespace: str) -> Tree:
    root, _ = _build_namespace(namespace, indexed=False)
    return root


def build_namespace_indexed_tree(
    namespace: str,
) -> tuple[Tree, list[tuple[str, str]]]:
    return _build_namespace(namespace, indexed=True)


def _build(
    kind: str, name: str, namespace: str, indexed: bool
) -> tuple[Tree, list[tuple[str, str]]]:
    load_config()
    resources: list[tuple[str, str]] = [(name, kind)] if indexed else []
    label = f"[header]{kind}/{name}[/header]"
    if indexed:
        label = f"[muted]1[/muted] {label}"
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
            root.add(f"[muted](no ownership graph for {kind})[/muted]")

    return root, resources


# Prefix + semantic color for each node kind in a namespace forest. Controllers
# render as roots with their full kind name; owned resources use the short
# prefixes the single-resource tree already uses (rs/job/pod).
_NODE_STYLE: dict[Kind, tuple[str, str]] = {
    Kind.Deployment: ("Deployment", "header"),
    Kind.StatefulSet: ("StatefulSet", "header"),
    Kind.DaemonSet: ("DaemonSet", "header"),
    Kind.CronJob: ("CronJob", "header"),
    Kind.Job: ("job", "accent"),
    Kind.ReplicaSet: ("rs", "accent"),
    Kind.Pod: ("pod", "body"),
}

# Stable order roots appear under the Namespace node.
_ROOT_ORDER: list[Kind] = [
    Kind.Deployment,
    Kind.StatefulSet,
    Kind.DaemonSet,
    Kind.CronJob,
    Kind.Job,
    Kind.ReplicaSet,
    Kind.Pod,
]


def _build_namespace(
    namespace: str, indexed: bool
) -> tuple[Tree, list[tuple[str, str]]]:
    """Full ownership forest for a namespace: every workload controller as a
    root with its owned resources beneath, plus orphan Jobs/ReplicaSets and bare
    Pods so nothing is hidden. Each pod appears exactly once. Unlike `_build`,
    the Namespace root node is not indexed — children number from 1."""
    load_config()
    apps = client.AppsV1Api()
    core = client.CoreV1Api()
    batch = client.BatchV1Api()

    deployments = apps.list_namespaced_deployment(namespace).items
    replica_sets = apps.list_namespaced_replica_set(namespace).items
    stateful_sets = apps.list_namespaced_stateful_set(namespace).items
    daemon_sets = apps.list_namespaced_daemon_set(namespace).items
    cron_jobs = batch.list_namespaced_cron_job(namespace).items
    jobs = batch.list_namespaced_job(namespace).items
    pods = core.list_namespaced_pod(namespace).items

    # Resources that can be owned by something else in the namespace, indexed by
    # each owner uid so a parent finds its children in one pass.
    child_kinds = (
        [(rs, Kind.ReplicaSet) for rs in replica_sets]
        + [(job, Kind.Job) for job in jobs]
        + [(pod, Kind.Pod) for pod in pods]
    )
    children_by_owner: dict[str, list[tuple[object, Kind]]] = {}
    for obj, kind in child_kinds:
        for ref in obj.metadata.owner_references or []:
            children_by_owner.setdefault(ref.uid, []).append((obj, kind))

    present_uids = {
        obj.metadata.uid
        for obj in (
            *deployments,
            *replica_sets,
            *stateful_sets,
            *daemon_sets,
            *cron_jobs,
            *jobs,
            *pods,
        )
    }

    # Controllers are always roots; an owned child (rs/job/pod) is a root only
    # when orphaned — its owner isn't among the namespace's collected objects.
    roots: list[tuple[object, Kind]] = [
        *[(d, Kind.Deployment) for d in deployments],
        *[(s, Kind.StatefulSet) for s in stateful_sets],
        *[(d, Kind.DaemonSet) for d in daemon_sets],
        *[(c, Kind.CronJob) for c in cron_jobs],
    ]
    for obj, kind in child_kinds:
        refs = obj.metadata.owner_references or []
        if not any(ref.uid in present_uids for ref in refs):
            roots.append((obj, kind))

    order = {kind: position for position, kind in enumerate(_ROOT_ORDER)}
    roots.sort(key=lambda pair: (order[pair[1]], pair[0].metadata.name))

    resources: list[tuple[str, str]] = []
    root = Tree(f"[header]Namespace/{namespace}[/header]")
    if not roots:
        root.add("[muted](no workloads)[/muted]")
        return root, resources

    for obj, kind in roots:
        _render(obj, kind, root, children_by_owner, resources, indexed)
    return root, resources


def _render(obj, kind, parent, children_by_owner, resources, indexed):
    """Add `obj` under `parent`, then recurse into its owned children; a Pod is
    a leaf whose containers are listed directly."""
    prefix, color = _NODE_STYLE[kind]
    node = _add_node(parent, resources, indexed, color, prefix, obj.metadata.name, kind)
    if kind == Kind.Pod:
        _add_containers(obj, node)
        return
    for child, child_kind in children_by_owner.get(obj.metadata.uid, []):
        _render(child, child_kind, node, children_by_owner, resources, indexed)


def _add_node(parent, resources, indexed, color, prefix, name, kind):
    """Add a labelled child node; when indexed, prepend its 1-based index and
    record `(name, kind)` in `resources`."""
    label = f"[{color}]{prefix}/{name}[/{color}]"
    if indexed:
        label = f"[muted]{len(resources) + 1}[/muted] {label}"
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
            "accent",
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
            node, resources, indexed, "accent", "job", job.metadata.name, Kind.Job
        )
        _add_pods_for_owner(job.metadata.uid, pods, job_node, resources, indexed)


def _tree_service(name, namespace, node, core, resources, indexed):
    svc = core.read_namespaced_service(name, namespace)
    selector = svc.spec.selector
    if not selector:
        node.add("[muted](no selector)[/muted]")
        return
    label_selector = ",".join(f"{k}={v}" for k, v in selector.items())
    pods = core.list_namespaced_pod(namespace, label_selector=label_selector).items
    if not pods:
        node.add("[muted](no matching pods)[/muted]")
        return
    for pod in pods:
        pod_node = _add_node(
            node, resources, indexed, "body", "pod", pod.metadata.name, Kind.Pod
        )
        _add_containers(pod, pod_node)


def _add_pods_for_owner(owner_uid, pods, parent_node, resources, indexed):
    owned = [pod for pod in pods if _owned_by(pod, owner_uid)]
    for pod in owned:
        pod_node = _add_node(
            parent_node,
            resources,
            indexed,
            "body",
            "pod",
            pod.metadata.name,
            Kind.Pod,
        )
        _add_containers(pod, pod_node)


def _add_containers(pod, parent_node):
    for container in pod.spec.containers:
        parent_node.add(f"[muted]container: {container.name}[/muted]")


def _owned_by(resource, uid: str) -> bool:
    refs = resource.metadata.owner_references or []
    return any(ref.uid == uid for ref in refs)


def most_recent_job(owner_uid: str, jobs):
    """The most recently-created Job owned by `owner_uid` (a CronJob's uid),
    by `metadata.creation_timestamp`; None if it has never run. Shared by
    resolve_workload_pods and DiagnosticsService — CronJob health and pods
    are both scoped to the latest run, not the full retained history."""
    owned = [job for job in jobs if _owned_by(job, owner_uid)]
    if not owned:
        return None
    return max(owned, key=lambda job: job.metadata.creation_timestamp)


def resolve_workload_pods(
    kind: str, name: str, namespace: str, apps, core, batch=None
) -> list:
    """Resolve the pods belonging to a workload via ownership references.

    Deployment is a two-hop walk (Deployment → owned ReplicaSets → owned pods,
    which includes surge/old pods mid-rollout); StatefulSet/DaemonSet/Job own
    their pods directly; Pod resolves to itself. Reuses `_owned_by` and fetches
    pods once per namespace, filtering client-side (mirrors the tree
    builders). `batch` is required for Job, unused otherwise."""
    if kind == Kind.Pod:
        return [core.read_namespaced_pod(name, namespace)]
    if kind == Kind.Service:
        svc = core.read_namespaced_service(name, namespace)
        selector = svc.spec.selector
        if not selector:
            return []
        label_selector = ",".join(f"{k}={v}" for k, v in selector.items())
        return core.list_namespaced_pod(namespace, label_selector=label_selector).items

    pods = core.list_namespaced_pod(namespace).items
    match kind:
        case Kind.Deployment:
            deploy = apps.read_namespaced_deployment(name, namespace)
            rs_uids = {
                rs.metadata.uid
                for rs in apps.list_namespaced_replica_set(namespace).items
                if _owned_by(rs, deploy.metadata.uid)
            }
            return [pod for pod in pods if any(_owned_by(pod, uid) for uid in rs_uids)]
        case Kind.StatefulSet:
            sts = apps.read_namespaced_stateful_set(name, namespace)
            return [pod for pod in pods if _owned_by(pod, sts.metadata.uid)]
        case Kind.DaemonSet:
            ds = apps.read_namespaced_daemon_set(name, namespace)
            return [pod for pod in pods if _owned_by(pod, ds.metadata.uid)]
        case Kind.Job:
            job = batch.read_namespaced_job(name, namespace)
            return [pod for pod in pods if _owned_by(pod, job.metadata.uid)]
        case Kind.CronJob:
            cj = batch.read_namespaced_cron_job(name, namespace)
            recent = most_recent_job(
                cj.metadata.uid, batch.list_namespaced_job(namespace).items
            )
            if recent is None:
                return []
            return [pod for pod in pods if _owned_by(pod, recent.metadata.uid)]
        case _:
            return []
