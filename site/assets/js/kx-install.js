// The landing page's install cards: a copy button on each, and the Binary
// card's platform choice.
//
// Everything here is an enhancement over markup that already works without
// it. The copy buttons and the platform chips ship `hidden` and are revealed
// only once this runs, so a reader without JavaScript never meets a control
// that does nothing, and the Binary card's command defaults to linux_amd64,
// which is correct as written for the most common case.
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

  // A browser reports the operating system reliably and the CPU hardly at
  // all: Safari on an Apple silicon Mac still says "Intel". So the default is
  // the likely case for each OS, and the chips correct it in one click.
  function guessPlatform() {
    var ua = navigator.userAgent || "";
    var platform = (navigator.userAgentData && navigator.userAgentData.platform) ||
      navigator.platform || "";
    if (/Mac/i.test(platform) || /Macintosh/i.test(ua)) return "darwin_arm64";
    if (/Linux/i.test(platform) && /aarch64|arm64|armv8/i.test(platform + " " + ua)) {
      return "linux_arm64";
    }
    return "linux_amd64";
  }

  function wirePlatforms(card) {
    var group = card.querySelector(".kx-install__platforms");
    var asset = card.querySelector("[data-kx-asset]");
    if (!group || !asset) return;
    var chips = group.querySelectorAll("[data-kx-platform]");

    function choose(name) {
      asset.textContent = "kx_" + name;
      chips.forEach(function (chip) {
        chip.setAttribute("aria-pressed", String(chip.dataset.kxPlatform === name));
      });
    }

    chips.forEach(function (chip) {
      chip.addEventListener("click", function () { choose(chip.dataset.kxPlatform); });
    });
    group.hidden = false;
    choose(guessPlatform());
  }

  function init() {
    document.querySelectorAll("[data-kx-copy]").forEach(function (card) {
      wireCopy(card);
      wirePlatforms(card);
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
