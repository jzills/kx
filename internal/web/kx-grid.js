/* kx-grid.js wires vendored Tabulator (see vendor/tabulator/) into whichever
 * grid mount(s) the current page has. layout.gohtml is shared by all three
 * --html pages, so every function here starts by checking its mount exists
 * and returns immediately if it doesn't — a diag single-resource page has no
 * "diag" mount at all, for instance.
 *
 * No build step: this runs in the browser exactly as written, matching the
 * repo's zero-toolchain constraint (see CLAUDE.md).
 */
(function () {
  "use strict";

  function kxData(id) {
    var el = document.getElementById(id);
    if (!el) return null;
    return JSON.parse(el.textContent);
  }

  function textCell(value) {
    return document.createTextNode(value === null || value === undefined ? "" : String(value));
  }

  // preserveScroll stops a redraw (a header filter changing, a group-by
  // toggle, a tree search) from moving the page under the user.
  //
  // Two distinct ways a redraw does this, both from the same root cause:
  // this grid's height is exactly its rendered content's height (kx-grid.css
  // strips the fixed-height scroll viewport Tabulator would otherwise
  // reserve, so the page scrolls instead of the grid), and every redraw
  // clears rendered rows before rebuilding them (Tabulator's own base render
  // path, confirmed in the vendored source — not something renderVertical:
  // "basic" opts out of).
  //
  //   1. Filtering to few/zero rows shrinks the document below the current
  //      scroll position. The browser clamps window.scrollY to fit, same as
  //      scrolling past the end of any page that just got shorter, and does
  //      not restore the clamped position once the rows regrow.
  //   2. Filtering out rows ABOVE whatever the user is currently looking at
  //      (scrolled past the grid's own top, into its later rows or groups)
  //      shrinks the document there instead. window.scrollY does not even
  //      need to change for this one — the same numeric scrollY now simply
  //      points at different content, because everything below the removed
  //      rows shifted up to fill the gap. Native CSS scroll anchoring exists
  //      for exactly this, but it anchors to a specific DOM node, and every
  //      row node here gets destroyed and rebuilt on each redraw, breaking
  //      that anchor.
  //
  // Both are fixed the same way: track whichever row is at the top of the
  // viewport by its data (the same object reference across a redraw —
  // Tabulator does not clone row data on a filter/group change, only on a
  // genuine setData() with new objects), and once the redraw completes,
  // measure where that same row ended up and scroll by the difference. This
  // corrects for both cases at once, because it responds to the row's actual
  // observed movement rather than assuming which of the two caused it.
  //
  // The mount's height is still locked from renderStarted to renderComplete
  // — necessary because case 1's browser clamp fires as soon as the
  // document gets too short to hold the *current* scroll position, which
  // can happen mid-redraw before there is a rebuilt row left to anchor to.
  // Locking the footprint up front means nothing can be clamped out from
  // under the anchor calculation before it runs. If literally no rows
  // survive the filter, there is no row left to anchor to at all — the lock
  // is left in place rather than released, so a filter matching nothing
  // does not collapse the grid (and move the page) out from under the user
  // either; the empty-state placeholder just renders inside the reserved
  // space instead.
  function preserveScroll(mount, table) {
    // A single "topmost visible row" anchor is not enough: if a filter
    // removes rows unevenly (more above the viewport than below it, or vice
    // versa — the normal case, not an edge case), that exact row can vanish
    // while the redraw is still something the user should see hold still.
    // Restoring window.scrollY as a fallback then does not work either,
    // because it does not account for how much content actually vanished
    // near the viewport specifically — it can still land dozens or hundreds
    // of pixels off (measured directly: a surviving row drifted 271px this
    // way in testing). Instead, every candidate anchor's row currently
    // rendered is captured, closest-to-viewport-top first, and whichever
    // one of them is still there after the redraw is used — the closest
    // survivor is the best available stand-in for "the row the user was
    // actually looking at."
    var anchorCandidates = [];
    // Remembers scrollY across a run of redraws that all match nothing —
    // set the moment such a run begins, cleared once content comes back and
    // the lock is released. No row-level anchor can help across that run:
    // with zero active rows there is nothing to capture as a candidate, on
    // every redraw in the run, not just the first. Without this, the
    // redraw where content finally reappears would see zero candidates —
    // indistinguishable from a page that was never scrolled into this grid
    // at all — and release the lock without correcting anything.
    var heldScrollY = null;

    function candidatesByProximityToTop() {
      var rows = table.getRows("active");
      var candidates = [];
      for (var i = 0; i < rows.length; i++) {
        var el = rows[i].getElement();
        if (!el) continue;
        candidates.push({ data: rows[i].getData(), top: el.getBoundingClientRect().top });
      }
      candidates.sort(function (a, b) { return Math.abs(a.top) - Math.abs(b.top); });
      return candidates;
    }

    table.on("renderStarted", function () {
      mount.style.minHeight = mount.getBoundingClientRect().height + "px";
      anchorCandidates = candidatesByProximityToTop();
      if (heldScrollY === null) heldScrollY = window.scrollY;
    });

    table.on("renderComplete", function () {
      var rows = table.getRows("active");
      // Nothing survived the filter at all. Checked first, ahead of the
      // candidate search below: once a redraw already collapses to zero
      // rows, the *next* redraw's renderStarted has nothing to capture as a
      // candidate either, so treating an empty candidate list the same as
      // "nothing was ever in view" would release the lock on every
      // subsequent keystroke while still matching nothing. Keeping the lock
      // here instead means a filter matching nothing does not collapse the
      // grid — and move the page — out from under the user; the empty-state
      // placeholder renders inside the reserved space.
      if (rows.length === 0) return;
      var relocated = null;
      var anchorTop = null;
      for (var c = 0; c < anchorCandidates.length && !relocated; c++) {
        for (var i = 0; i < rows.length; i++) {
          if (rows[i].getData() === anchorCandidates[c].data) {
            relocated = rows[i];
            anchorTop = anchorCandidates[c].top;
            break;
          }
        }
      }
      if (relocated) {
        var delta = relocated.getElement().getBoundingClientRect().top - anchorTop;
        if (delta !== 0) window.scrollBy(0, delta);
      } else if (heldScrollY !== null && window.scrollY !== heldScrollY) {
        // No candidate row survived at all — either nothing was in view to
        // capture candidates from in the first place, every candidate got
        // filtered out, or content is only just reappearing after a
        // matched-nothing run. The lock has kept the document tall enough
        // for heldScrollY to still be a valid position in every one of
        // those cases where it can be; restoring it directly is a safe
        // no-op otherwise. If the surviving content genuinely cannot
        // support heldScrollY any more, releasing the lock right after
        // lets the browser's own clamp reduce it correctly — the same
        // "trust the native clamp once nothing else can help" a fully-empty
        // result already relies on.
        window.scrollTo(window.scrollX, heldScrollY);
      }
      mount.style.minHeight = "";
      heldScrollY = null;
    });
  }

  // uniqueValues builds a header-filter "list" values array from whatever
  // a field actually contains in this sweep, sorted for a stable menu
  // order — used for Kind, which (unlike Verdict) has no fixed, known set
  // of values to hand-write.
  function uniqueValues(rows, field) {
    var seen = {};
    var out = [];
    rows.forEach(function (row) {
      var v = row[field];
      if (v && !seen[v]) {
        seen[v] = true;
        out.push(v);
      }
    });
    out.sort();
    return out;
  }

  // A multiselect list filter's own display field is a genuine single-line
  // <input> (confirmed in the vendored source — Tabulator's list editor
  // always creates one, filter or not), which cannot wrap onto multiple
  // lines the way a <textarea> could; that's an HTML constraint no CSS
  // reaches, not a styling gap. This is the pragmatic middle ground: keep
  // selections readable via a native tooltip once they overflow, rather
  // than either leaving them silently cut off or rebuilding the whole
  // filter as a custom widget. Tabulator has no per-column "filter
  // changed" event in this build (checked: only the table-wide
  // dataFiltered exists), so this re-syncs every header-filter input's
  // title on every filter change rather than just the one that moved.
  function syncFilterTooltips(mount) {
    mount.querySelectorAll(".tabulator-header-filter input").forEach(function (input) {
      input.title = input.value;
    });
  }

  // Mirrors severityClass/severityIcon in page.go (diagnostics.Severity:
  // OK=0, Warning=1, Critical=2) so the grid's verdict badge reads the same
  // as the report it expands into. Kept here, not derived from the server,
  // because a formatter runs client-side with no access to Go closures.
  function verdictClass(rank) {
    if (rank >= 2) return "status-bad";
    if (rank === 1) return "status-warn";
    return "status-ok";
  }
  function verdictIcon(rank) {
    if (rank >= 2) return "✗"; // matches severityIcon's Critical marker
    if (rank === 1) return "!";
    return "✓";
  }

  // Mirrors scan.gohtml's inline severity-to-class logic for the findings
  // table (CRITICAL/HIGH -> bad, MEDIUM -> warn, else muted).
  function findingSeverityClass(severity) {
    if (severity === "CRITICAL" || severity === "HIGH") return "status-bad";
    if (severity === "MEDIUM") return "status-warn";
    return "dim";
  }
  var severityOrder = ["CRITICAL", "HIGH", "MEDIUM", "LOW", "UNSPECIFIED"];
  function severityRank(severity) {
    var i = severityOrder.indexOf(severity);
    return i === -1 ? severityOrder.length : i;
  }

  function addControls(mount, children) {
    var wrap = document.createElement("div");
    wrap.className = "kx-grid-controls";
    children.forEach(function (child) { wrap.appendChild(child); });
    mount.parentNode.insertBefore(wrap, mount);
    return wrap;
  }

  // Not a native <select>: a native select's open option list is genuine
  // OS/browser chrome, which CSS can only reach as far as background-color/
  // color on <option> — nowhere near Kind/Verdict's actual look, since
  // those are Tabulator's own List editor (a custom-built .tabulator-
  // edit-list popup, confirmed in the vendored source's
  // _createListElement). Built as the same DOM shape instead — a
  // .tabulator-popup-container wrapping a .tabulator-edit-list of
  // .tabulator-edit-list-item rows — so it inherits every rule this
  // stylesheet already has for that popup, with nothing new to keep in
  // sync if that theming changes later.
  function groupSelect(table, options) {
    var wrap = document.createElement("span");
    wrap.className = "kx-group-select";

    var label = document.createElement("label");
    var labelText = document.createElement("span");
    labelText.className = "kx-control-label";
    labelText.textContent = "Group by";
    label.appendChild(labelText);
    wrap.appendChild(label);

    var allOptions = [{ field: "", label: "None" }].concat(options);
    var current = allOptions[0];
    allOptions.forEach(function (opt) { if (opt.selected) current = opt; });

    var trigger = document.createElement("button");
    trigger.type = "button";
    trigger.className = "kx-group-trigger";
    trigger.textContent = current.label;
    label.appendChild(trigger);

    var popup = null;

    function close() {
      if (!popup) return;
      popup.remove();
      popup = null;
      document.removeEventListener("mousedown", onDocClick, true);
    }

    function onDocClick(e) {
      if (popup && !popup.contains(e.target) && e.target !== trigger) close();
    }

    function choose(opt) {
      current = opt;
      trigger.textContent = opt.label;
      table.setGroupBy(opt.field || false);
      close();
    }

    function open() {
      if (popup) { close(); return; }
      popup = document.createElement("div");
      popup.className = "tabulator-popup-container kx-group-popup";
      var list = document.createElement("div");
      list.className = "tabulator-edit-list";
      allOptions.forEach(function (opt) {
        var item = document.createElement("div");
        item.className = "tabulator-edit-list-item" + (opt === current ? " active" : "");
        item.textContent = opt.label;
        item.addEventListener("mousedown", function (e) {
          e.preventDefault(); // mousedown, not click: fires before onDocClick's capture-phase listener would even matter, so the choice registers in one event rather than racing the close-on-outside-click logic
          choose(opt);
        });
        list.appendChild(item);
      });
      popup.appendChild(list);
      wrap.appendChild(popup);
      // Capture phase: this listener must see the click before it reaches
      // whatever it landed on, since the trigger's own click (which comes
      // after mousedown) would otherwise immediately reopen what this just
      // closed.
      document.addEventListener("mousedown", onDocClick, true);
    }

    trigger.addEventListener("click", open);

    return wrap;
  }

  // ---- diag -----------------------------------------------------------

  function diagRowFormatter(row) {
    var data = row.getData();
    var el = row.getElement();
    var existing = el.querySelector(".kx-detail");
    if (data._expanded) {
      if (!existing) {
        var tpl = document.getElementById("diag-detail-" + data.Row);
        if (tpl) {
          var wrap = document.createElement("div");
          wrap.className = "kx-detail";
          wrap.appendChild(tpl.content.cloneNode(true));
          el.appendChild(wrap);
        }
      }
    } else if (existing) {
      existing.remove();
    }
  }

  function verdictFormatter(cell) {
    var d = cell.getData();
    var span = document.createElement("span");
    span.className = "verdict " + verdictClass(d.VerdictRank);
    span.textContent = verdictIcon(d.VerdictRank) + " " + d.Verdict;
    return span;
  }

  function initDiagGrid() {
    var mount = document.querySelector('[data-kx-grid="diag"]');
    if (!mount) return;
    var data = kxData("kx-diag-data");
    if (!data) return;
    var allNamespaces = mount.getAttribute("data-all-namespaces") === "true";

    var columns = [];
    if (!allNamespaces) {
      // "X", not "#": every indexed listing in kx (internal/render/history.go,
      // triage.go, listing.go) heads its index column "X" — this grid
      // shouldn't invent a different convention for the same concept.
      // width 72, not the letter's own width: the header cell also holds
      // the sort-direction arrow, and the two together were crowding "X"
      // into a text-overflow ellipsis at 56.
      columns.push({ title: "X", field: "Index", width: 72, hozAlign: "right", sorter: "number" });
    }
    columns.push({
      title: "Kind", field: "Kind", width: 175,
      headerFilter: "list",
      // multiselect: true makes the filter value an array of the selected
      // values, but does NOT change how that value gets matched against a
      // row — Tabulator's default header-filter comparator (confirmed in
      // the vendored source) does something exact-match-shaped with it,
      // which against an array is never true for a single-valued field.
      // That's what read as "AND" (more selections, never a match) and
      // "everything empty after clearing" (a leftover non-empty array
      // still failing every row). headerFilterFunc: "in" is Tabulator's
      // own built-in comparator built for exactly this shape — array,
      // OR-membership, empty array matches everything — found in the same
      // filters map as "like"/"starts"/"regex".
      headerFilterFunc: "in",
      // No synthetic "All" entry: with nothing selected the filter array
      // is empty, and "in" already treats that as "show everything," so
      // "All" would be a checkbox meaning the same thing as checking
      // nothing rather than a real, distinct option. headerFilterPlaceholder
      // says the same thing passively, as placeholder text, rather than as
      // a selectable item.
      headerFilterPlaceholder: "All",
      headerFilterParams: { values: uniqueValues(data, "Kind"), multiselect: true, clearable: true },
    });
    if (allNamespaces) {
      columns.push({
        title: "Namespace", field: "Namespace", width: 170,
        headerFilter: "input", headerFilterPlaceholder: "Type to filter…",
      });
    }
    columns.push({
      title: "Name", field: "Name", headerFilter: "input", headerFilterPlaceholder: "Type to filter…",
      widthGrow: 2,
    });
    columns.push({
      title: "Verdict", field: "Verdict", width: 165,
      formatter: verdictFormatter,
      sorter: function (_a, _b, aRow, bRow) {
        return aRow.getData().VerdictRank - bRow.getData().VerdictRank;
      },
      headerFilter: "list",
      // Same "in" comparator fix as Kind above, same reasoning.
      headerFilterFunc: "in",
      // Same multiselect reasoning as Kind above: no synthetic "All" entry,
      // an empty selection already means every verdict shows.
      headerFilterPlaceholder: "All",
      headerFilterParams: {
        values: { healthy: "Healthy", warnings: "Warning", critical: "Critical" },
        multiselect: true,
        clearable: true,
      },
    });
    columns.push({
      title: "Top finding", field: "TopFinding", widthGrow: 3,
      headerFilter: "input", headerFilterPlaceholder: "Type to filter…",
      formatter: function (cell) { return textCell(cell.getValue()); },
    });

    var table = new Tabulator(mount, {
      data: data,
      layout: "fitColumns",
      columns: columns,
      initialSort: [{ column: "Verdict", dir: "desc" }],
      rowFormatter: diagRowFormatter,
      // Tabulator's default virtual-DOM renderer calculates every row's
      // height once, up front, to absolutely position rows and size the
      // scroll container — it has no way to know a row grew after
      // diagRowFormatter appends a detail panel on click. row.reformat()
      // only re-runs the formatter for that one row; it doesn't trigger the
      // table-wide re-layout the virtual renderer would need to stop
      // clipping the newly taller row at its stale height. "basic" mode
      // renders every row as real DOM with natural height, which is what
      // this grid needs since rows resize after the initial render — this
      // grid is a namespace sweep (tens to low hundreds of rows), not the
      // scale virtual scrolling exists for.
      renderVertical: "basic",
    });

    preserveScroll(mount, table);
    table.on("dataFiltered", function () { syncFilterTooltips(mount); });

    var groupOptions = allNamespaces
      ? [{ field: "Namespace", label: "Namespace" }, { field: "Kind", label: "Kind" }, { field: "Verdict", label: "Verdict" }]
      : [{ field: "Kind", label: "Kind" }, { field: "Verdict", label: "Verdict" }];
    addControls(mount, [groupSelect(table, groupOptions)]);

    table.on("rowClick", function (_e, row) {
      var d = row.getData();
      if (!document.getElementById("diag-detail-" + d.Row)) return;
      d._expanded = !d._expanded;
      row.reformat();
    });
  }

  // ---- scan -------------------------------------------------------------

  function severityBarFormatter(cell) {
    var bands = cell.getValue() || [];
    var wrap = document.createElement("span");
    wrap.className = "kx-bar";
    bands.forEach(function (seg) {
      var i = document.createElement("i");
      i.className = seg.Class;
      i.style.width = seg.Pct + "%";
      wrap.appendChild(i);
    });
    return wrap;
  }

  function imageStatusFormatter(cell) {
    var d = cell.getData();
    var span = document.createElement("span");
    if (d.Error) {
      span.className = "status-warn";
      span.textContent = "scan failed";
    } else if (d.Critical > 0) {
      span.className = "status-bad";
      span.textContent = "critical";
    } else if (d.High > 0 || d.Medium > 0) {
      span.className = "status-warn";
      span.textContent = "warnings";
    } else {
      span.className = "status-ok";
      span.textContent = "clean";
    }
    return span;
  }

  function countFormatter(cssWhenPositive) {
    return function (cell) {
      var d = cell.getData();
      var span = document.createElement("span");
      if (d.Error) {
        span.className = "dim";
        span.textContent = "—"; // em dash, matches the existing "no data" convention
        return span;
      }
      var v = cell.getValue();
      span.textContent = v;
      span.className = cssWhenPositive && v > 0 ? cssWhenPositive : "dim";
      return span;
    };
  }

  function initScanImageGrid() {
    var mount = document.querySelector('[data-kx-grid="scan-images"]');
    if (!mount) return;
    var data = kxData("kx-scan-images-data");
    if (!data) return;

    var table = new Tabulator(mount, {
      data: data,
      layout: "fitColumns",
      columns: [
        { title: "Image", field: "Image", widthGrow: 3, headerFilter: "input", headerFilterPlaceholder: "Type to filter…" },
        { title: "Severity", field: "Bar", formatter: severityBarFormatter, widthGrow: 2, headerSort: false },
        { title: "Crit", field: "Critical", width: 70, hozAlign: "right", sorter: "number", formatter: countFormatter("status-bad") },
        { title: "High", field: "High", width: 70, hozAlign: "right", sorter: "number", formatter: countFormatter("status-bad") },
        { title: "Med", field: "Medium", width: 70, hozAlign: "right", sorter: "number", formatter: countFormatter("status-warn") },
        { title: "Low", field: "Low", width: 70, hozAlign: "right", sorter: "number", formatter: countFormatter(null) },
        { title: "Unspec", field: "Unspecified", width: 80, hozAlign: "right", sorter: "number", formatter: countFormatter(null) },
        { title: "Status", field: "Error", width: 120, formatter: imageStatusFormatter, headerSort: false },
      ],
      initialSort: [{ column: "Critical", dir: "desc" }],
      // See the renderVertical note by .tabulator-tableholder in
      // kx-grid.css: this page's CSS strips the virtual renderer's own
      // scroll viewport, so it must not be used here.
      renderVertical: "basic",
    });
    preserveScroll(mount, table);
  }

  // Scanner output is not trusted input — a compromised or malformed
  // scanner could hand back a "javascript:" URL. The server-side JSON
  // encoding neutralises HTML metacharacters (see marshalJS in page.go) but
  // has no notion of URL safety, so that check belongs here, right before
  // the value becomes a real href. Mirrors html/template's own
  // http(s)-only allowlist for the same field in the pre-grid rendering.
  function isSafeLink(url) {
    return /^https?:\/\//i.test(url);
  }

  function cveFormatter(cell) {
    var d = cell.getData();
    if (d.URL && isSafeLink(d.URL)) {
      var a = document.createElement("a");
      a.href = d.URL;
      a.rel = "noreferrer noopener";
      a.target = "_blank";
      a.textContent = d.ID;
      return a;
    }
    return textCell(d.ID);
  }

  function severityFormatter(cell) {
    var value = cell.getValue();
    var span = document.createElement("span");
    span.className = findingSeverityClass(value);
    span.textContent = value;
    return span;
  }

  function fixedInFormatter(cell) {
    var value = cell.getValue();
    var span = document.createElement("span");
    if (value) {
      span.className = "status-ok";
      span.textContent = value;
    } else {
      span.className = "dim";
      span.textContent = "—";
    }
    return span;
  }

  function initScanFindingsGrid() {
    var mount = document.querySelector('[data-kx-grid="scan-findings"]');
    if (!mount) return;
    var data = kxData("kx-scan-findings-data");
    if (!data) return;

    var table = new Tabulator(mount, {
      data: data,
      layout: "fitColumns",
      groupBy: "Image",
      placeholder: "No vulnerabilities found",
      columns: [
        {
          title: "CVE", field: "ID", widthGrow: 1,
          headerFilter: "input", headerFilterPlaceholder: "Type to filter…",
          formatter: cveFormatter,
        },
        { title: "Image", field: "Image", widthGrow: 2, headerFilter: "input", headerFilterPlaceholder: "Type to filter…" },
        {
          title: "Severity", field: "Severity", width: 120, formatter: severityFormatter,
          sorter: function (a, b) { return severityRank(a) - severityRank(b); },
          headerFilter: "list",
          // Same multiselect/"in"/placeholder pattern as diag's Kind and
          // Verdict columns: an empty selection already shows everything, so
          // there is no synthetic "All" entry, only placeholder text saying
          // the same thing passively.
          headerFilterFunc: "in",
          headerFilterPlaceholder: "All",
          headerFilterParams: {
            values: { CRITICAL: "Critical", HIGH: "High", MEDIUM: "Medium", LOW: "Low", UNSPECIFIED: "Unspecified" },
            multiselect: true,
            clearable: true,
          },
        },
        { title: "Package", field: "Package", widthGrow: 2, headerFilter: "input", headerFilterPlaceholder: "Type to filter…" },
        { title: "Installed", field: "Installed", widthGrow: 1 },
        { title: "Fixed in", field: "FixedIn", widthGrow: 1, formatter: fixedInFormatter },
        {
          title: "Fixable", field: "Fixable", width: 90, hozAlign: "center",
          formatter: function (cell) { return cell.getValue() ? "Yes" : "No"; },
          headerFilter: "list",
          // Same placeholder pattern as Severity above: no synthetic "All"
          // entry — clearing the selection (clearable: true) already yields
          // the "" headerValue headerFilterFunc treats as "show everything",
          // so "All" is placeholder text, not a real option to pick.
          headerFilterPlaceholder: "All",
          headerFilterParams: { values: { true: "Fixable", false: "No fix" }, clearable: true },
          headerFilterFunc: function (headerValue, rowValue) {
            return headerValue === "" || String(rowValue) === headerValue;
          },
        },
      ],
      initialSort: [{ column: "Severity", dir: "asc" }],
      // See the renderVertical note by .tabulator-tableholder in
      // kx-grid.css: this page's CSS strips the virtual renderer's own
      // scroll viewport, so it must not be used here.
      renderVertical: "basic",
    });
    preserveScroll(mount, table);

    addControls(mount, [
      groupSelect(table, [
        { field: "Image", label: "Image", selected: true },
        { field: "Severity", label: "Severity" },
      ]),
    ]);
  }

  // ---- tree ---------------------------------------------------------------

  // Search is implemented by pruning a copy of the tree client-side and
  // re-setting the table's data, rather than relying on Tabulator's built-in
  // dataTree filter — that filter's default behaviour hides a parent outright
  // when it fails the test even if a descendant matches, which is the
  // opposite of "search collapses to non-matches, keeps matching branches
  // expanded." Pruning our own copy sidesteps that entirely: a node survives
  // if it matches or any descendant does, and dataTreeStartExpanded keeps
  // every surviving branch open.
  function pruneTree(nodes, query) {
    if (!query) return nodes;
    var q = query.toLowerCase();
    var kept = [];
    nodes.forEach(function (node) {
      var children = node._children ? pruneTree(node._children, query) : [];
      var selfMatch = node.Label.toLowerCase().indexOf(q) !== -1;
      if (selfMatch || children.length) {
        var copy = {};
        for (var key in node) {
          if (Object.prototype.hasOwnProperty.call(node, key)) copy[key] = node[key];
        }
        if (children.length) {
          copy._children = children;
        } else {
          delete copy._children;
        }
        kept.push(copy);
      }
    });
    return kept;
  }

  function treeLabelFormatter(cell) {
    var d = cell.getData();
    var span = document.createElement("span");
    span.className = "tree-label style-" + (d.Style || "");
    span.textContent = d.Label;
    return span;
  }

  function searchInput(onInput) {
    var label = document.createElement("label");
    var labelText = document.createElement("span");
    labelText.className = "kx-control-label";
    labelText.textContent = "Search";
    label.appendChild(labelText);
    var input = document.createElement("input");
    input.type = "search";
    input.className = "kx-search-input";
    input.placeholder = "filter by name…";
    input.addEventListener("input", function () { onInput(input.value); });
    label.appendChild(input);
    return label;
  }

  function initTreeGrid() {
    var mount = document.querySelector('[data-kx-grid="tree"]');
    if (!mount) return;
    var data = kxData("kx-tree-data");
    if (!data) return;

    var table = new Tabulator(mount, {
      data: data,
      layout: "fitColumns",
      dataTree: true,
      dataTreeChildField: "_children",
      dataTreeStartExpanded: true,
      dataTreeElementColumn: "Label",
      columns: [
        {
          // "X", matching every other indexed listing in kx — see the
          // same note on the diag grid's Index column above. width 72 for
          // the same reason: the header's sort arrow crowds a 1-character
          // title into an ellipsis at anything narrower.
          title: "X", field: "Index", width: 72, hozAlign: "right",
          formatter: function (cell) { var v = cell.getValue(); return v > 0 ? String(v) : ""; },
        },
        { title: "Name", field: "Label", formatter: treeLabelFormatter, widthGrow: 3 },
      ],
      // See the renderVertical note by .tabulator-tableholder in
      // kx-grid.css: this page's CSS strips the virtual renderer's own
      // scroll viewport, so it must not be used here.
      renderVertical: "basic",
    });
    preserveScroll(mount, table);

    addControls(mount, [
      searchInput(function (value) {
        table.setData(pruneTree(data, value));
      }),
    ]);
  }

  // The browser scrolls an off-screen input into view on its own, in two
  // separate cases that have nothing to do with Tabulator and nothing
  // preserveScroll's renderStarted/renderComplete hooks can see, since no
  // redraw is involved in either: focusing it (confirmed: clicking a filter
  // scrolled past the grid's own header jumps the page even before typing
  // anything), and — separately — every keystroke it receives afterward, to
  // keep the caret visible (confirmed by dispatching keystrokes with no
  // Playwright element-targeting involved at all: the very first one still
  // moved the page). A header filter sits at the top of a grid whose own
  // rows can run hundreds of pixels below the current scroll position (see
  // preserveScroll's own comment on why this page scrolls instead of the
  // grid), so scrolling past it and then clicking or typing into it is not
  // a rare edge case here.
  //
  // The click case has a real fix, not a guess: preventDefault() on
  // mousedown stops the browser's own default focus (and the scroll that
  // comes with it), and focus({preventScroll: true}) grants focus back
  // without it — the standard technique for exactly this, and precise
  // rather than reactive.
  document.addEventListener("mousedown", function (e) {
    var input = e.target.closest(".tabulator-header-filter input");
    if (!input) return;
    e.preventDefault();
    input.focus({ preventScroll: true });
  }, true);

  // The keystroke case has no equivalent preventable event — it is the
  // browser's own "keep the caret visible" behavior tied to the input's
  // value changing, not a focus call this code makes. So this one really
  // is reactive: a trusted scrollY is tracked continuously from ordinary
  // scrolling, and any scroll landing shortly after a keydown on a header
  // filter is reverted to that trusted value instead of adopted as the new
  // one. 150ms is short enough that a real follow-up scroll (a human
  // reacting and then moving a wheel) essentially never lands inside it,
  // while comfortably covering the keystroke's own scroll, which resolves
  // within a frame or two — confirmed by testing that a shorter window
  // (50ms) still missed it under real per-keystroke round-trip timing.
  var trustedScrollX = window.scrollX;
  var trustedScrollY = window.scrollY;
  var suppressUntil = 0;

  document.addEventListener("keydown", function (e) {
    if (e.target.closest(".tabulator-header-filter")) suppressUntil = Date.now() + 150;
  }, true);

  window.addEventListener("scroll", function () {
    if (Date.now() < suppressUntil) {
      window.scrollTo(trustedScrollX, trustedScrollY);
      return;
    }
    trustedScrollX = window.scrollX;
    trustedScrollY = window.scrollY;
  });

  document.addEventListener("DOMContentLoaded", function () {
    if (typeof Tabulator === "undefined") return;
    initDiagGrid();
    initScanImageGrid();
    initScanFindingsGrid();
    initTreeGrid();
  });
})();
