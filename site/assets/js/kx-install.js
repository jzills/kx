// A copy button on each install command, the landing page's cards and the
// footer's one-liner, and the tabs that turn the cards into one panel.
//
// An enhancement over markup that already works without it. The buttons ship
// `hidden` and are revealed only once this runs, so a reader without
// JavaScript never meets a control that does nothing; without it the cards
// stay a grid, every method visible at once.
(function () {
  "use strict";

  var DONE_MS = 1500;

  // What a card copies is exactly what it shows, one command per line. The
  // "$ " prompt is drawn by CSS (::before), so it is not in textContent and
  // never lands in the clipboard.
  function commandsOf(card) {
    var lines = [];
    card.querySelectorAll(".kx-install__command").forEach(function (el) {
      lines.push(el.textContent.trim());
    });
    return lines.join("\n");
  }

  // Where the Clipboard API is unavailable — a page served over plain HTTP,
  // say — select the commands instead, so a manual copy takes one keystroke.
  function selectCommands(card) {
    var commands = card.querySelectorAll(".kx-install__command");
    if (!commands.length || !window.getSelection) return;
    var range = document.createRange();
    range.setStartBefore(commands[0]);
    range.setEndAfter(commands[commands.length - 1]);
    var selection = window.getSelection();
    selection.removeAllRanges();
    selection.addRange(range);
  }

  function announce(card, button, message) {
    var status = card.querySelector(".kx-install__status");
    if (status) status.textContent = message;
    button.classList.add("is-done");
    clearTimeout(button._kxDone);
    button._kxDone = setTimeout(function () {
      button.classList.remove("is-done");
      if (status) status.textContent = "";
    }, DONE_MS);
  }

  function wireCopy(card) {
    var button = card.querySelector(".kx-install__copy");
    if (!button) return;
    button.hidden = false;
    button.addEventListener("click", function () {
      var text = commandsOf(card);
      if (navigator.clipboard && window.isSecureContext) {
        navigator.clipboard.writeText(text).then(
          function () { announce(card, button, "Copied"); },
          function () { selectCommands(card); }
        );
      } else {
        selectCommands(card);
      }
    });
  }

  // wireTabs turns a grid of install cards into one tabbed panel: a tab per
  // card, named by the card's own label, so the tab list cannot drift from
  // the methods the cards show. One card at a time reads as one command to
  // run, where four side by side read as a choice to make first.
  function wireTabs(container) {
    var cards = Array.prototype.slice.call(
      container.querySelectorAll(".kx-install__card")
    );
    if (cards.length < 2) return;

    var list = document.createElement("div");
    list.className = "kx-tabs";
    list.setAttribute("role", "tablist");
    list.setAttribute("aria-label", container.getAttribute("aria-label") || "Install with");

    var tabs = cards.map(function (card, i) {
      var label = card.querySelector(".kx-install__label");
      var id = "kx-install-" + i;
      card.id = id + "-panel";
      card.setAttribute("role", "tabpanel");
      card.setAttribute("aria-labelledby", id);

      var tab = document.createElement("button");
      tab.type = "button";
      tab.id = id;
      tab.className = "kx-tabs__tab";
      tab.setAttribute("role", "tab");
      tab.setAttribute("aria-controls", card.id);
      tab.textContent = label ? label.textContent.trim() : "Option " + (i + 1);
      list.appendChild(tab);
      return tab;
    });

    function select(index, focus) {
      tabs.forEach(function (tab, i) {
        var on = i === index;
        tab.setAttribute("aria-selected", on ? "true" : "false");
        tab.tabIndex = on ? 0 : -1;
        cards[i].hidden = !on;
      });
      if (focus) tabs[index].focus();
    }

    tabs.forEach(function (tab, i) {
      tab.addEventListener("click", function () { select(i, false); });
      tab.addEventListener("keydown", function (event) {
        var next = null;
        if (event.key === "ArrowRight") next = (i + 1) % tabs.length;
        if (event.key === "ArrowLeft") next = (i - 1 + tabs.length) % tabs.length;
        if (event.key === "Home") next = 0;
        if (event.key === "End") next = tabs.length - 1;
        if (next !== null) {
          event.preventDefault();
          select(next, true);
        }
      });
    });

    container.classList.add("is-tabbed");
    container.insertBefore(list, container.firstChild);
    select(0, false);
  }

  function init() {
    document.querySelectorAll("[data-kx-copy]").forEach(wireCopy);
    document.querySelectorAll("[data-kx-tabs]").forEach(wireTabs);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
