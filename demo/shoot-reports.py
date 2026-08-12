#!/usr/bin/env python3
"""Screenshot the --html reports, once per kx palette, for the site.

    demo/shoot-reports.py                    # every report, every palette
    demo/shoot-reports.py --report diag      # one report
    demo/shoot-reports.py --theme dracula    # one palette

Each report is served by kx itself: `kx <report> --html --no-open --port N`
binds localhost and prints the URL, which is what gets loaded. That means the
screenshots are of the real page kx generates, in the palette kx generated it
for, rather than of a mock-up of one.

Prereqs: a seeded cluster with the current namespace set to `diagnostics` (see
demo/seed.sh), and playwright with chromium. The environment for that is not
committed — it is a screenshotting tool, not part of building kx:

    python3 -m venv /tmp/shotenv
    /tmp/shotenv/bin/pip install playwright
    /tmp/shotenv/bin/playwright install chromium
    /tmp/shotenv/bin/python demo/shoot-reports.py
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
# assets/, not static/: Hugo can read an image's real dimensions from a
# resource, and the shots are each a different height now that they are trimmed
# to a whole grid row. A static/ file cannot be introspected, so the shortcode
# would have to hardcode a size and reserve the wrong space.
OUTPUT_DIR = ROOT / "site" / "assets" / "img"
THEME_DATA = ROOT / "site" / "data" / "kx_themes.json"

# The reports the site shows. `scan` is included but needs its engine's CLI, so
# it is skipped rather than failed when the engine is unavailable — a missing
# scanner should not stop the other two being regenerated.
REPORTS = {
    "diag": ["diag", "--html", "--no-open"],
    "tree": ["tree", "--html", "--no-open"],
    "scan": ["scan", "--html", "--no-open"],
}

# Wide enough that the tables do not collapse to their narrow layout, and tall
# enough to show a screenful without capturing the whole scroll height.
VIEWPORT = {"width": 1440, "height": 900}


def palettes() -> list[str]:
    """The palettes the site offers, from the generated registry export."""
    return [entry["name"] for entry in json.loads(THEME_DATA.read_text())]


def free_port() -> int:
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 0))
        return probe.getsockname()[1]


def build_kx(into: Path) -> Path:
    """Build the working tree's kx, so the shot matches the code being committed."""
    binary = into / "kx"
    subprocess.run(
        ["go", "build", "-o", str(binary), "./cmd/kx"], cwd=ROOT, check=True
    )
    return binary


URL_PATTERN = re.compile(r"https?://[0-9.]+:\d+\S*")


def serve(binary: Path, report: str, theme: str, port: int):
    """Start kx serving a report and return (process, url)."""
    env = {**os.environ, "KX_THEME": theme, "NO_COLOR": "1"}
    process = subprocess.Popen(
        [str(binary), *REPORTS[report], "--port", str(port)],
        cwd=ROOT,
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        # Own process group, so the whole thing can be signalled: the server
        # runs until interrupted and ignoring that leaves a port bound.
        start_new_session=True,
    )

    # kx prints the URL once the server is up; waiting for the line is what
    # makes this reliable rather than sleeping and hoping.
    deadline = time.time() + 120
    while time.time() < deadline:
        line = process.stdout.readline()
        if not line:
            if process.poll() is not None:
                raise RuntimeError(f"kx {report} exited before serving")
            continue
        match = URL_PATTERN.search(line)
        if match:
            return process, match.group(0)
    raise TimeoutError(f"kx {report} never printed a URL")


def stop(process) -> None:
    os.killpg(os.getpgid(process.pid), signal.SIGINT)
    try:
        process.wait(timeout=15)
    except subprocess.TimeoutExpired:
        os.killpg(os.getpgid(process.pid), signal.SIGKILL)


# Trim the shot to the last grid row that fits entirely on screen.
#
# A viewport-sized screenshot slices whichever row straddles the bottom edge,
# which reads as a broken image rather than as a report that continues. Ending
# on a row boundary looks deliberate. Tabulator draws every row — tree nodes,
# CVEs, sweep findings — as .tabulator-row, and its group headers as
# .tabulator-group, so one rule covers all three reports.
LAST_ROW_BOTTOM = """
(maxHeight) => {
  let bottom = 0;
  const rows = document.querySelectorAll('.tabulator-row, .tabulator-group');
  for (const row of rows) {
    const edge = row.getBoundingClientRect().bottom;
    if (edge <= maxHeight && edge > bottom) bottom = edge;
  }
  return Math.round(bottom);
}
"""

# How much of the viewport bottom to ignore when looking for the last row, so
# the search never picks one whose separator would still be clipped.
ROW_MARGIN = 10


def shoot(page, url: str, destination: Path) -> None:
    page.goto(url, wait_until="networkidle")
    # The grids hydrate after load; without settling, a shot can catch the
    # table mid-render.
    page.wait_for_timeout(600)

    height = VIEWPORT["height"]
    bottom = page.evaluate(LAST_ROW_BOTTOM, height - ROW_MARGIN)
    # A report with no grid at all — or one whose first row is already taller
    # than the viewport — keeps the full frame rather than an empty sliver.
    if bottom > height // 2:
        # Exactly the row's edge, with nothing added: even ten pixels past it
        # shows the top sliver of the next row, which is the thing that reads
        # as a broken image rather than as a report that continues below.
        height = bottom

    destination.parent.mkdir(parents=True, exist_ok=True)
    page.screenshot(
        path=str(destination),
        clip={"x": 0, "y": 0, "width": VIEWPORT["width"], "height": height},
    )


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report", choices=sorted(REPORTS), action="append")
    parser.add_argument("--theme", action="append")
    args = parser.parse_args()

    reports = args.report or sorted(REPORTS)
    themes = args.theme or palettes()

    try:
        from playwright.sync_api import sync_playwright
    except ImportError:
        print(__doc__, file=sys.stderr)
        return 2

    workdir = Path(tempfile.mkdtemp(prefix="kx-shots-"))
    try:
        binary = build_kx(workdir)
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch()
            page = browser.new_page(viewport=VIEWPORT)

            for report in reports:
                for theme in themes:
                    destination = OUTPUT_DIR / f"{report}-html-{theme}.png"
                    try:
                        process, url = serve(binary, report, theme, free_port())
                    except Exception as error:  # noqa: BLE001 - reported, not raised
                        print(f"skip {report}/{theme}: {error}", file=sys.stderr)
                        continue
                    try:
                        shoot(page, url, destination)
                        print(f"wrote {destination.relative_to(ROOT)}")
                    finally:
                        stop(process)

            browser.close()
    finally:
        shutil.rmtree(workdir, ignore_errors=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
