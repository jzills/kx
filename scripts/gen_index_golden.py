"""Regenerate the Go port's index parity fixtures from the Python implementation.

The Go index package asserts byte-for-byte equality against this output, so the
two implementations cannot drift on column widths, padding, dedupe order, or the
no-match fallback.

    python scripts/gen_index_golden.py > internal/index/testdata/python_golden.json

Delete this script along with the Python implementation at cutover.
"""

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "src"))

from kx.index import IndexService  # noqa: E402

FIXTURES = {
    "pods": (
        "NAME             READY   STATUS    RESTARTS   AGE\n"
        "nginx-abc-xyz    1/1     Running   0          5d\n"
        "redis-def-uvw    1/1     Running   0          3d"
    ),
    "single": (
        "NAME             READY   STATUS    RESTARTS   AGE\n"
        "only-pod-abc     1/1     Running   0          1d"
    ),
    # kubectl doesn't pad a table's last column, so a value there can be wider
    # than its header word.
    "contexts": (
        "CURRENT   NAME             CLUSTER          AUTHINFO         NAMESPACE\n"
        "*         docker-desktop   docker-desktop   docker-desktop   diagnostics"
    ),
    "duplicates": (
        "NAME      READY   STATUS\n"
        "pod-a     1/1     Running\n"
        "pod-a     0/1     Pending\n"
        "pod-b     1/1     Running"
    ),
    "header_only": "NAME             READY   STATUS    RESTARTS   AGE",
    "wide_values": (
        "NAME                                     READY   STATUS             RESTARTS   AGE\n"
        "very-long-deployment-name-abcdef-12345   0/1     CrashLoopBackOff   17         12d\n"
        "s                                        1/1     Running            0          1s"
    ),
    "json_output": '{"apiVersion": "v1", "kind": "List", "items": []}',
    "empty": "",
}


def main() -> None:
    service = IndexService()
    golden = {}
    for name, text in FIXTURES.items():
        added, names = service.add(text)
        golden[name] = {
            "input": text,
            "add": added,
            "names": names,
            "filter_a": service.filter(text, "a"),
            "filter_none": service.filter(text, "zzz-no-match"),
        }
    print(json.dumps(golden, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
