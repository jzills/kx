// The landing page's sections ease up into place as they scroll into view.
//
// An enhancement only. The sections are visible in the markup and the CSS
// hides them only under html.kx-motion, which this script sets — so without
// JavaScript, without IntersectionObserver, or for a reader who has asked
// for reduced motion, nothing is ever hidden and nothing moves.
(function () {
  "use strict";

  var reduced = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  if (reduced || !("IntersectionObserver" in window)) return;

  var targets = document.querySelectorAll("[data-kx-reveal]");
  if (!targets.length) return;

  var observer = new IntersectionObserver(function (entries) {
    entries.forEach(function (entry) {
      if (!entry.isIntersecting) return;
      entry.target.classList.add("is-revealed");
      observer.unobserve(entry.target);
    });
  }, { rootMargin: "0px 0px -10% 0px" });

  // A section already on screen when the page loads (a reload halfway down,
  // a jump to #install) is shown at once rather than faded in.
  var fold = window.innerHeight;
  targets.forEach(function (target) {
    if (target.getBoundingClientRect().top < fold) {
      target.classList.add("is-revealed");
    }
  });

  document.documentElement.classList.add("kx-motion");
  targets.forEach(function (target) {
    if (!target.classList.contains("is-revealed")) observer.observe(target);
  });
})();
