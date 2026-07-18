from enum import StrEnum


class Kind(StrEnum):
    Pod = "Pod"
    Deployment = "Deployment"
    ReplicaSet = "ReplicaSet"
    StatefulSet = "StatefulSet"
    DaemonSet = "DaemonSet"
    CronJob = "CronJob"
    Service = "Service"
    HorizontalPodAutoscaler = "HorizontalPodAutoscaler"
    Ingress = "Ingress"
    ConfigMap = "ConfigMap"
    Secret = "Secret"
    Job = "Job"
    PersistentVolumeClaim = "PersistentVolumeClaim"
    Node = "Node"
    Namespace = "Namespace"


_KIND_MAP: dict[str, Kind] = {
    "po": Kind.Pod,
    "pod": Kind.Pod,
    "pods": Kind.Pod,
    "deployment": Kind.Deployment,
    "deployments": Kind.Deployment,
    "deploy": Kind.Deployment,
    "replicaset": Kind.ReplicaSet,
    "replicasets": Kind.ReplicaSet,
    "rs": Kind.ReplicaSet,
    "statefulset": Kind.StatefulSet,
    "statefulsets": Kind.StatefulSet,
    "sts": Kind.StatefulSet,
    "daemonset": Kind.DaemonSet,
    "daemonsets": Kind.DaemonSet,
    "ds": Kind.DaemonSet,
    "hpa": Kind.HorizontalPodAutoscaler,
    "horizontalpodautoscaler": Kind.HorizontalPodAutoscaler,
    "horizontalpodautoscalers": Kind.HorizontalPodAutoscaler,
    "service": Kind.Service,
    "services": Kind.Service,
    "svc": Kind.Service,
    "ingress": Kind.Ingress,
    "ingresses": Kind.Ingress,
    "configmap": Kind.ConfigMap,
    "configmaps": Kind.ConfigMap,
    "cm": Kind.ConfigMap,
    "secret": Kind.Secret,
    "secrets": Kind.Secret,
    "job": Kind.Job,
    "jobs": Kind.Job,
    "cronjob": Kind.CronJob,
    "cronjobs": Kind.CronJob,
    "pvc": Kind.PersistentVolumeClaim,
    "persistentvolumeclaim": Kind.PersistentVolumeClaim,
    "persistentvolumeclaims": Kind.PersistentVolumeClaim,
    "node": Kind.Node,
    "nodes": Kind.Node,
    "namespace": Kind.Namespace,
    "namespaces": Kind.Namespace,
}


_PLURAL_DISPLAY: dict[Kind, str] = {
    Kind.Pod: "Pods",
    Kind.Deployment: "Deployments",
    Kind.ReplicaSet: "ReplicaSets",
    Kind.StatefulSet: "StatefulSets",
    Kind.DaemonSet: "DaemonSets",
    Kind.CronJob: "CronJobs",
    Kind.Service: "Services",
    Kind.HorizontalPodAutoscaler: "HorizontalPodAutoscalers",
    Kind.Ingress: "Ingresses",
    Kind.ConfigMap: "ConfigMaps",
    Kind.Secret: "Secrets",
    Kind.Job: "Jobs",
    Kind.PersistentVolumeClaim: "PersistentVolumeClaims",
    Kind.Node: "Nodes",
    Kind.Namespace: "Namespaces",
}


def is_kind_spelling(token: str) -> bool:
    return token.lower() in _KIND_MAP


def normalize_kind(resource_type: str) -> Kind | str:
    return _KIND_MAP.get(resource_type.lower(), resource_type)


def plural_display(resource_type: str) -> str:
    kind = _KIND_MAP.get(resource_type.lower())
    if kind is not None:
        return _PLURAL_DISPLAY.get(kind, kind.value + "s")
    return resource_type
