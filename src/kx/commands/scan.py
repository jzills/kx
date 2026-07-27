import json
from contextlib import nullcontext

from kx.kinds import Kind
from kx.kubectl import KubectlServiceProtocol
from kx.scanner import Engine, ImageScan, ScannerServiceProtocol, get_engine
from kx.state import StateServiceProtocol
from kx.types import Status

_SUPPORTED_KINDS = {
    Kind.Pod,
    Kind.Deployment,
    Kind.ReplicaSet,
    Kind.StatefulSet,
    Kind.DaemonSet,
    Kind.Job,
    Kind.CronJob,
}

# Workload kinds swept for a namespace-level scan, as a single kubectl selector.
_NAMESPACE_KINDS = "deployments,statefulsets,daemonsets,cronjobs,jobs,pods"


class ScanCommand:
    def __init__(
        self,
        state: StateServiceProtocol,
        kubectl: KubectlServiceProtocol,
        scanner: ScannerServiceProtocol,
        status: Status | None = None,
    ):
        self.state = state
        self.kubectl = kubectl
        self.scanner = scanner
        self.status = status or (lambda _msg: nullcontext())

    def execute(
        self, index: int, engine: str = "scout", extra_args: list[str] | None = None
    ) -> list[str]:
        name, namespace, kind = self.state.fields(index)
        if kind not in _SUPPORTED_KINDS:
            raise ValueError(f"scan is not supported for '{kind}'.")
        # Validate the engine and its availability before hitting the cluster,
        # so a typo or a missing scanner fails fast with one clear message.
        self.ensure_available(engine)
        with self.status("resolving images"):
            raw = self.kubectl.run(["get", kind, name, "-n", namespace, "-o", "json"])
        images = _dedupe(self._images_of(json.loads(raw)))
        if not images:
            raise ValueError(f"no container images found for {kind}/{name}.")
        return images

    def collect_namespace(
        self, namespace: str, engine: str = "scout", extra_args: list[str] | None = None
    ) -> list[str]:
        self.ensure_available(engine)
        with self.status(f"resolving images in {namespace}"):
            raw = self.kubectl.run(
                ["get", _NAMESPACE_KINDS, "-n", namespace, "-o", "json"]
            )
        items = json.loads(raw).get("items", [])
        images = _dedupe([image for item in items for image in self._images_of(item)])
        if not images:
            raise ValueError(f"no container images found in namespace '{namespace}'.")
        return images

    def ensure_available(self, engine: str) -> Engine:
        """Resolve the engine and confirm the scanner is installed, so the
        failure is reported once with a fix rather than per image."""
        eng = get_engine(engine)
        if self.scanner.probe(eng.preflight_argv()) != 0:
            raise RuntimeError(eng.unavailable_message())
        return eng

    def scan_image(
        self, engine: str, image: str, extra_args: list[str] | None = None
    ) -> int:
        return self.scanner.scan(get_engine(engine).passthrough_argv(image, extra_args))

    def summarize(self, engine: str, images: list[str]) -> list[ImageScan]:
        eng = get_engine(engine)
        rows = []
        for image in images:
            result = self.scanner.capture(eng.summary_argv(image))
            if result.returncode != 0:
                reason = (result.stderr or "").strip().splitlines()
                rows.append(
                    ImageScan(image, error=reason[-1] if reason else "scan failed")
                )
                continue
            try:
                counts = eng.parse_counts(result.stdout)
            except (ValueError, KeyError, TypeError):
                rows.append(ImageScan(image, error="unparseable output"))
                continue
            rows.append(ImageScan(image, counts=counts))
        return rows

    @classmethod
    def _images_of(cls, obj: dict) -> list[str]:
        pod_spec = cls._pod_spec(obj)
        return [
            container["image"]
            for group in ("initContainers", "containers")
            for container in (pod_spec.get(group) or [])
            if container.get("image")
        ]

    @staticmethod
    def _pod_spec(obj: dict) -> dict:
        """Locate the PodSpec for any workload kind. Deployments/StatefulSets/
        DaemonSets/Jobs/ReplicaSets carry it under spec.template.spec; a CronJob
        nests it under spec.jobTemplate.spec.template.spec; a bare Pod is spec."""
        spec = obj.get("spec") or {}
        if obj.get("kind") == Kind.CronJob:
            return (
                spec.get("jobTemplate", {})
                .get("spec", {})
                .get("template", {})
                .get("spec")
                or {}
            )
        template = spec.get("template")
        if template is not None:
            return template.get("spec") or {}
        return spec


def _dedupe(images: list[str]) -> list[str]:
    """Unique, preserving first-seen order."""
    return list(dict.fromkeys(images))
