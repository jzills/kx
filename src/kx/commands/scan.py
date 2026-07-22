import json
from contextlib import nullcontext

from kx.kinds import Kind
from kx.kubectl import KubectlServiceProtocol
from kx.scanner import ScannerServiceProtocol, build_engine_argv
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
        # Validate the engine before hitting the cluster so a typo fails fast.
        build_engine_argv(engine, "", extra_args)
        with self.status("resolving images"):
            raw = self.kubectl.run(["get", kind, name, "-n", namespace, "-o", "json"])
        images = self._extract_images(json.loads(raw))
        if not images:
            raise ValueError(f"no container images found for {kind}/{name}.")
        return images

    def scan_image(
        self, engine: str, image: str, extra_args: list[str] | None = None
    ) -> int:
        return self.scanner.scan(build_engine_argv(engine, image, extra_args))

    @staticmethod
    def _extract_images(obj: dict) -> list[str]:
        spec = obj.get("spec", {})
        # Workloads carry the container spec under spec.template.spec; a bare Pod
        # carries it directly under spec.
        pod_spec = spec.get("template", {}).get("spec") if "template" in spec else spec
        pod_spec = pod_spec or {}
        images = [
            container["image"]
            for group in ("initContainers", "containers")
            for container in (pod_spec.get(group) or [])
            if container.get("image")
        ]
        # Unique, preserving first-seen order.
        return list(dict.fromkeys(images))
