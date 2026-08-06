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

  function groupSelect(table, options) {
    var label = document.createElement("label");
    label.appendChild(document.createTextNode("Group by "));
    var select = document.createElement("select");
    var none = document.createElement("option");
    none.value = "";
    none.textContent = "(none)";
    select.appendChild(none);
    options.forEach(function (opt) {
      var o = document.createElement("option");
      o.value = opt.field;
      o.textContent = opt.label;
      if (opt.selected) o.selected = true;
      select.appendChild(o);
    });
    select.addEventListener("change", function () {
      table.setGroupBy(select.value || false);
    });
    label.appendChild(select);
    return label;
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
      columns.push({ title: "#", field: "Index", width: 56, hozAlign: "right", sorter: "number" });
    }
    columns.push({
      title: "Kind", field: "Kind", width: 150,
      headerFilter: "list", headerFilterParams: { valuesLookup: true, clearable: true },
    });
    if (allNamespaces) {
      columns.push({ title: "Namespace", field: "Namespace", width: 170, headerFilter: "input" });
    }
    columns.push({ title: "Name", field: "Name", headerFilter: "input", widthGrow: 2 });
    columns.push({
      title: "Verdict", field: "Verdict", width: 140,
      formatter: verdictFormatter,
      sorter: function (_a, _b, aRow, bRow) {
        return aRow.getData().VerdictRank - bRow.getData().VerdictRank;
      },
      headerFilter: "list",
      headerFilterParams: {
        values: { "": "All", healthy: "Healthy", warnings: "Warning", critical: "Critical" },
        clearable: true,
      },
    });
    columns.push({
      title: "Top finding", field: "TopFinding", widthGrow: 3, headerFilter: "input",
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

    new Tabulator(mount, {
      data: data,
      layout: "fitColumns",
      columns: [
        { title: "Image", field: "Image", widthGrow: 3, headerFilter: "input" },
        { title: "Severity", field: "Bar", formatter: severityBarFormatter, widthGrow: 2, headerSort: false },
        { title: "Crit", field: "Critical", width: 70, hozAlign: "right", sorter: "number", formatter: countFormatter("status-bad") },
        { title: "High", field: "High", width: 70, hozAlign: "right", sorter: "number", formatter: countFormatter("status-bad") },
        { title: "Med", field: "Medium", width: 70, hozAlign: "right", sorter: "number", formatter: countFormatter("status-warn") },
        { title: "Low", field: "Low", width: 70, hozAlign: "right", sorter: "number", formatter: countFormatter(null) },
        { title: "Unspec", field: "Unspecified", width: 80, hozAlign: "right", sorter: "number", formatter: countFormatter(null) },
        { title: "Status", field: "Error", width: 120, formatter: imageStatusFormatter, headerSort: false },
      ],
      initialSort: [{ column: "Critical", dir: "desc" }],
    });
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
        { title: "CVE", field: "ID", widthGrow: 1, headerFilter: "input", formatter: cveFormatter },
        { title: "Image", field: "Image", widthGrow: 2, headerFilter: "input" },
        {
          title: "Severity", field: "Severity", width: 120, formatter: severityFormatter,
          sorter: function (a, b) { return severityRank(a) - severityRank(b); },
          headerFilter: "list",
          headerFilterParams: {
            values: { "": "All", CRITICAL: "Critical", HIGH: "High", MEDIUM: "Medium", LOW: "Low", UNSPECIFIED: "Unspecified" },
            clearable: true,
          },
        },
        { title: "Package", field: "Package", widthGrow: 2, headerFilter: "input" },
        { title: "Installed", field: "Installed", widthGrow: 1 },
        { title: "Fixed in", field: "FixedIn", widthGrow: 1, formatter: fixedInFormatter },
        {
          title: "Fixable", field: "Fixable", width: 90, hozAlign: "center",
          formatter: function (cell) { return cell.getValue() ? "Yes" : "No"; },
          headerFilter: "list",
          headerFilterParams: { values: { "": "All", true: "Fixable", false: "No fix" }, clearable: true },
          headerFilterFunc: function (headerValue, rowValue) {
            return headerValue === "" || String(rowValue) === headerValue;
          },
        },
      ],
      initialSort: [{ column: "Severity", dir: "asc" }],
    });

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
    label.appendChild(document.createTextNode("Search "));
    var input = document.createElement("input");
    input.type = "search";
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
          title: "#", field: "Index", width: 64, hozAlign: "right",
          formatter: function (cell) { var v = cell.getValue(); return v > 0 ? String(v) : ""; },
        },
        { title: "Name", field: "Label", formatter: treeLabelFormatter, widthGrow: 3 },
      ],
    });

    addControls(mount, [
      searchInput(function (value) {
        table.setData(pruneTree(data, value));
      }),
    ]);
  }

  document.addEventListener("DOMContentLoaded", function () {
    if (typeof Tabulator === "undefined") return;
    initDiagGrid();
    initScanImageGrid();
    initScanFindingsGrid();
    initTreeGrid();
  });
})();
