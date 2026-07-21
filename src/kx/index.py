import re
from typing import TYPE_CHECKING, Protocol

if TYPE_CHECKING:
    from kx.state import State


def resolve_index(state, index: int) -> str:
    names = list(state.resources.keys())
    count = len(names)
    if not 1 <= index <= count:
        label = "item" if count == 1 else "items"
        raise ValueError(
            f"Index {index} is out of range — current state has {count} {label} "
            f"(run 'kx state' to view)."
        )
    return names[index - 1]


def _parse_output(output: str) -> tuple[list[str], list[list[str]], int]:
    lines = output.splitlines()
    if not lines:
        return [], [], 0

    header = lines[0]
    spans = [
        (col_match.start(), col_match.end())
        for col_match in re.finditer(r"\S+\s*", header)
    ]
    if spans:
        # kubectl doesn't pad a table's last column with trailing spaces, so
        # its span (derived from the header word's own width) can be
        # narrower than a data row's actual value in that column. Extend it
        # to end-of-line so wider values aren't sliced off.
        last_start, _ = spans[-1]
        spans[-1] = (last_start, None)
    headers = [header[start:end].strip() for start, end in spans]
    if "NAME" not in headers:
        return [], [], 0
    name_idx = headers.index("NAME")

    rows = []
    for row in lines[1:]:
        if not row.strip():
            continue
        cols = [row[start:end].strip() for start, end in spans]
        if len(cols) <= name_idx:
            continue
        rows.append(cols)

    return headers, rows, name_idx


class IndexServiceProtocol(Protocol):
    def add(self, output: str) -> tuple[str, list[str]]: ...
    def filter(self, output: str, term: str) -> str: ...
    def resolve(self, state: "State", index: int) -> str: ...


class IndexService:
    def add(self, output: str) -> tuple[str, list[str]]:
        headers, rows, name_idx = _parse_output(output)
        if not headers:
            return output, []

        names = [row[name_idx] for row in rows]

        headers = ["X"] + headers
        rows = [[str(i + 1)] + row for i, row in enumerate(rows)]

        all_rows = [headers] + rows
        cols = list(zip(*all_rows))
        widths = [max(len(cell) for cell in col) for col in cols]

        def fmt(row: list[str]) -> str:
            return "  ".join(cell.ljust(widths[i]) for i, cell in enumerate(row))

        return "\n".join(fmt(row) for row in all_rows), names

    def filter(self, output: str, term: str) -> str:
        headers, rows, name_idx = _parse_output(output)
        if not headers:
            return output

        filtered = [row for row in rows if term.lower() in row[name_idx].lower()]

        all_rows = [headers] + filtered
        if len(all_rows) == 1:
            return output.splitlines()[0]

        cols = list(zip(*all_rows))
        widths = [max(len(cell) for cell in col) for col in cols]

        def fmt(row: list[str]) -> str:
            return "  ".join(cell.ljust(widths[i]) for i, cell in enumerate(row))

        return "\n".join(fmt(row) for row in all_rows)

    def resolve(self, state: "State", index: int) -> str:
        return resolve_index(state, index)
