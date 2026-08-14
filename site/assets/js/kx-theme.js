{{- /*
  The palette runtime: one apply(), shared by everything that can change or
  display the active kx palette.

  Inlined into <head> by custom/head-end.html rather than loaded as a file.
  It has to run before the first paint — Hextra's own theme.js is a head
  script that sets a light/dark class from the Hugo default, and correcting
  that after the page has painted shows the wrong navbar for as long as the
  rest of the page takes to load. A deferred <script src> could not do that.

  Templated by Hugo so the palette table comes from data/kx_themes.json,
  which tools/gen-site-theme writes from internal/theme. Every control reads
  this one list, so a palette cannot exist in the picker and not the menu.
*/ -}}
{{- $favicon := "" -}}
{{- with resources.Get "favicon.svg" }}{{ $favicon = .Content }}{{ end -}}
(function () {
  // name → {mode, accent}, plus the ordered list the controls render.
  var PALETTES = [
    {{- range hugo.Data.kx_themes }}
    {
      name: {{ .name | jsonify | safeJS }},
      mode: {{ .mode | jsonify | safeJS }},
      accent: {{ .accent | jsonify | safeJS }},
      background: {{ .background | jsonify | safeJS }}
    },
    {{- end }}
  ];

  // The mark, as shipped. Repainted per palette rather than redrawn in
  // JavaScript: a second copy of the letterforms is the drift internal/mark
  // exists to prevent. Only the fill changes, which is the one thing a
  // palette has an opinion about.
  var FAVICON = {{ $favicon | jsonify | safeJS }};

  // Matches DEFAULT_THEME in the controls and defaultDark in
  // tools/gen-site-theme.
  var DEFAULT = "github-dark";

  // Our own key. "color-theme" below is Hextra's, and holds a mode rather
  // than a palette; see apply().
  var STORAGE_KEY = "kx-theme";

  var root = document.documentElement;
  var byName = {};
  PALETTES.forEach(function (palette) {
    byName[palette.name] = palette;
  });

  var listeners = [];

  function paintFavicon(accent) {
    var link = document.getElementById("favicon-svg");
    if (!link || !FAVICON || !accent) {
      return;
    }
    var painted = FAVICON.replace(/fill="[^"]*"/, 'fill="' + accent + '"');
    link.setAttribute("href", "data:image/svg+xml," + encodeURIComponent(painted));
  }

  // apply paints a palette without recording the choice. Used for the
  // first paint, where the stored value is being restored rather than made.
  function apply(name) {
    var palette = byName[name] || byName[DEFAULT];

    root.setAttribute("data-kx-theme", palette.name);
    // Hextra's own chrome — the navbar, the sidebar — is styled against a
    // light/dark class, not against data-kx-theme, and colorScheme keeps
    // native form controls and scrollbars in step too. Without this half,
    // picking a light palette left the navbar dark.
    root.classList.remove("light", "dark");
    root.classList.add(palette.mode);
    root.style.colorScheme = palette.mode;

    paintFavicon(palette.accent);

    try {
      // Hextra ships a *body* script (core/theme.js) that reapplies a class
      // from this key on every load, unconditionally, falling back to the
      // Hugo-configured default when it is unset — which is what kept
      // resetting the class straight back after we got it right. Keeping
      // this key in step is what stops that.
      localStorage.setItem("color-theme", palette.mode);
    } catch (e) {
      /* Private browsing refuses storage; the palette still applies. */
    }

    listeners.forEach(function (listener) {
      listener(palette.name);
    });
    return palette.name;
  }

  // choose is apply plus persistence: what a control calls.
  function choose(name) {
    var chosen = apply(name);
    try {
      localStorage.setItem(STORAGE_KEY, chosen);
    } catch (e) {
      /* Same private-browsing case; the choice applies for this page. */
    }
    return chosen;
  }

  function stored() {
    try {
      return localStorage.getItem(STORAGE_KEY);
    } catch (e) {
      return null;
    }
  }

  window.kxTheme = {
    palettes: PALETTES,
    fallback: DEFAULT,
    current: function () {
      return root.getAttribute("data-kx-theme") || DEFAULT;
    },
    apply: apply,
    choose: choose,
    // Controls subscribe to render their own selected state. Called back
    // immediately so a control drawn after the first paint starts in step
    // rather than waiting for the next change.
    subscribe: function (listener) {
      listeners.push(listener);
      listener(window.kxTheme.current());
    }
  };

  apply(stored());
})();
