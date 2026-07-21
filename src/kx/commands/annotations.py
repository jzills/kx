from kx.commands._metadata import fetch_metadata_field
from kx.kubectl import KubectlServiceProtocol
from kx.state import StateServiceProtocol


class AnnotationsCommand:
    def __init__(self, state: StateServiceProtocol, kubectl: KubectlServiceProtocol):
        self.state = state
        self.kubectl = kubectl

    def execute(self, index: int) -> dict[str, str]:
        return fetch_metadata_field(self.kubectl, self.state, index, "annotations")
