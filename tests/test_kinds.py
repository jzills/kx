from kx.kinds import Kind, normalize_kind, plural_display


def test_kind_is_str():
    assert isinstance(Kind.Pod, str)


def test_normalize_kind_po():
    assert normalize_kind("po") == Kind.Pod


def test_normalize_kind_pod():
    assert normalize_kind("pod") == Kind.Pod


def test_normalize_kind_pods():
    assert normalize_kind("pods") == Kind.Pod


def test_normalize_kind_deploy():
    assert normalize_kind("deploy") == Kind.Deployment


def test_normalize_kind_deployments():
    assert normalize_kind("deployments") == Kind.Deployment


def test_normalize_kind_svc():
    assert normalize_kind("svc") == Kind.Service


def test_normalize_kind_rs():
    assert normalize_kind("rs") == Kind.ReplicaSet


def test_normalize_kind_sts():
    assert normalize_kind("sts") == Kind.StatefulSet


def test_normalize_kind_ds():
    assert normalize_kind("ds") == Kind.DaemonSet


def test_normalize_kind_hpa():
    assert normalize_kind("hpa") == Kind.HorizontalPodAutoscaler


def test_normalize_kind_cm():
    assert normalize_kind("cm") == Kind.ConfigMap


def test_normalize_kind_pvc():
    assert normalize_kind("pvc") == Kind.PersistentVolumeClaim


def test_normalize_kind_uppercase():
    assert normalize_kind("PODS") == Kind.Pod


def test_normalize_kind_mixed_case():
    assert normalize_kind("Deploy") == Kind.Deployment


def test_normalize_kind_unknown_passthrough():
    assert normalize_kind("UnknownThing") == "UnknownThing"


def test_normalize_kind_unknown_preserves_original_case():
    result = normalize_kind("MyCustomResource")
    assert result == "MyCustomResource"


def test_normalize_kind_cronjob():
    assert normalize_kind("cronjob") == Kind.CronJob


def test_normalize_kind_job():
    assert normalize_kind("job") == Kind.Job


def test_normalize_kind_ingress():
    assert normalize_kind("ingress") == Kind.Ingress


def test_normalize_kind_secret():
    assert normalize_kind("secret") == Kind.Secret


def test_normalize_kind_node():
    assert normalize_kind("node") == Kind.Node


def test_normalize_kind_namespace():
    assert normalize_kind("namespace") == Kind.Namespace


def test_normalize_kind_namespace_shorthand():
    # `kx get ns` must record Kind.Namespace so `kx ns <index>` accepts it.
    assert normalize_kind("ns") == Kind.Namespace


def test_plural_display_pods_is_pascal_case():
    assert plural_display("pods") == "Pods"


def test_plural_display_deployments_is_pascal_case():
    assert plural_display("deployments") == "Deployments"


def test_plural_display_unknown_passthrough():
    assert plural_display("mycrd") == "mycrd"
