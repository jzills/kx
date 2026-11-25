// The copy button on the footer's install command, on every project docs
// site. Shared, byte for byte, by kx, snoop, action-cache and hmac-manager:
// change it in one, copy it to all four. Linked from custom/head-end.html.
//
// An enhancement over markup that already works without it: the button
// ships `hidden` and is revealed only once this runs, so a reader without
// JavaScript never meets a control that does nothing. What it copies is
// exactly the command shown; the "$ " prompt is drawn by CSS, so it is not
// in textContent and never lands in the clipboard.
(function () {
  "use strict";

  var DONE_MS = 1500;

  // Where the Clipboard API is unavailable, a page served over plain HTTP
  // say, select the command instead, so a manual copy takes one keystroke.
  function select(text) {
    if (!window.getSelection) return;
    var range = document.createRange();
    range.selectNodeContents(text);
    var selection = window.getSelection();
    selection.removeAllRanges();
    selection.addRange(range);
  }

  document.querySelectorAll("[data-oss-copy]").forEach(function (box) {
    var text = box.querySelector(".oss-command__text");
    var button = box.querySelector(".oss-command__copy");
    var status = box.querySelector(".oss-command__status");
    if (!text || !button) return;
    button.hidden = false;
    button.addEventListener("click", function () {
      var done = function () {
        if (status) status.textContent = "Copied";
        button.classList.add("is-done");
        clearTimeout(button._ossDone);
        button._ossDone = setTimeout(function () {
          button.classList.remove("is-done");
          if (status) status.textContent = "";
        }, DONE_MS);
      };
      if (navigator.clipboard && window.isSecureContext) {
        navigator.clipboard.writeText(text.textContent.trim()).then(done, function () { select(text); });
      } else {
        select(text);
      }
    });
  });
})();
