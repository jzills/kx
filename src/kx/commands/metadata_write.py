from kx.commands._metadata import fetch_metadata_field
from kx.kubectl import KubectlServiceProtocol
from kx.state import StateServiceProtocol

_VERB_LABEL_TEXT = {"label": "Labeled", "annotate": "Annotated"}


class _MetadataWriteCommand:
    def __init__(
        self,
        kubectl: KubectlServiceProtocol,
        state: StateServiceProtocol,
        verb: str,
        field: str,
    ):
        self.kubectl = kubectl
        self.state = state
        self.verb = verb
        self.field = field

    def execute(
        self,
        index: int,
        sets: dict[str, str],
        removes: list[str],
        overwrite: bool,
    ) -> str:
        if not sets and not removes:
            raise ValueError("nothing to set or remove")

        name, namespace, kind = self.state.fields(index)

        current = fetch_metadata_field(self.kubectl, self.state, index, self.field)
        if not overwrite:
            conflicts = [key for key in sets if key in current]
            if conflicts:
                raise ValueError(
                    f"{', '.join(conflicts)} already set; use --overwrite to replace"
                )

        args = [self.verb, kind, name, "-n", namespace]
        args += [f"{key}={value}" for key, value in sets.items()]
        args += [f"{key}-" for key in removes]
        if overwrite:
            args.append("--overwrite")
        self.kubectl.run(args)

        parts = []
        if sets:
            parts.append(f"set {len(sets)}")
        if removes:
            parts.append(f"removed {len(removes)}")
        verb_text = _VERB_LABEL_TEXT[self.verb]
        return f"{verb_text} {kind}/{name} ({', '.join(parts)})"
