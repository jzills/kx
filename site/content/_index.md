---
title: "kx — kubectl, indexed"
toc: false
---

<div class="kx-hero">
  <span class="kx-hero__logo">{{< kx-mark >}}</span>
  <p class="kx-hero__tagline">kubectl, indexed.</p>
  <p class="kx-hero__blurb">
    Run <code>kx get &lt;resource&gt;</code> once and every row gets a number.
    From then on you reference resources by that number instead of typing
    names — <code>kx logs 2</code>, <code>kx delete 2 5</code>, <code>kx exec
    1</code>.
  </p>
</div>

<div class="kx-section">
{{< kx-hero-terminal >}}
</div>

<div class="kx-section" id="install">
  <h2 class="kx-section__title">Install</h2>
  <p class="kx-section__lede">
    Requires <code>kubectl</code> on your PATH. Every method below installs the
    same prebuilt binary — from PyPI too, with no Python runtime.
  </p>
  <div class="kx-install">
    <div class="kx-install__card" data-kx-copy>
      <div class="kx-install__head">
        <div class="kx-install__label">krew</div>
        <button type="button" class="kx-install__copy" aria-label="Copy the krew install commands" hidden><svg viewBox="0 0 16 16" aria-hidden="true" class="kx-install__icon kx-install__icon--copy"><rect x="5" y="5" width="9" height="9" rx="1.5"/><path d="M11 5V3.5A1.5 1.5 0 0 0 9.5 2h-6A1.5 1.5 0 0 0 2 3.5v6A1.5 1.5 0 0 0 3.5 11H5"/></svg><svg viewBox="0 0 16 16" aria-hidden="true" class="kx-install__icon kx-install__icon--done"><path d="M3 8.5 6.5 12 13 4.5"/></svg></button>
      </div>
      <div class="kx-install__command">kubectl krew install idx</div>
      <div class="kx-install__command">alias kx="kubectl idx"</div>
      <span class="kx-install__status" role="status"></span>
    </div>
    <div class="kx-install__card" data-kx-copy>
      <div class="kx-install__head">
        <div class="kx-install__label">uv</div>
        <button type="button" class="kx-install__copy" aria-label="Copy the uv install command" hidden><svg viewBox="0 0 16 16" aria-hidden="true" class="kx-install__icon kx-install__icon--copy"><rect x="5" y="5" width="9" height="9" rx="1.5"/><path d="M11 5V3.5A1.5 1.5 0 0 0 9.5 2h-6A1.5 1.5 0 0 0 2 3.5v6A1.5 1.5 0 0 0 3.5 11H5"/></svg><svg viewBox="0 0 16 16" aria-hidden="true" class="kx-install__icon kx-install__icon--done"><path d="M3 8.5 6.5 12 13 4.5"/></svg></button>
      </div>
      <div class="kx-install__command">uv tool install kx-cli</div>
      <span class="kx-install__status" role="status"></span>
    </div>
    <div class="kx-install__card kx-install__card--binary" data-kx-copy>
      <div class="kx-install__head">
        <div class="kx-install__label">Binary</div>
        <button type="button" class="kx-install__copy" aria-label="Copy the binary install commands" hidden><svg viewBox="0 0 16 16" aria-hidden="true" class="kx-install__icon kx-install__icon--copy"><rect x="5" y="5" width="9" height="9" rx="1.5"/><path d="M11 5V3.5A1.5 1.5 0 0 0 9.5 2h-6A1.5 1.5 0 0 0 2 3.5v6A1.5 1.5 0 0 0 3.5 11H5"/></svg><svg viewBox="0 0 16 16" aria-hidden="true" class="kx-install__icon kx-install__icon--done"><path d="M3 8.5 6.5 12 13 4.5"/></svg></button>
      </div>
      <div class="kx-install__platforms" role="group" aria-label="Platform" hidden>
        <button type="button" data-kx-platform="linux_amd64" aria-pressed="true">Linux x64</button>
        <button type="button" data-kx-platform="linux_arm64" aria-pressed="false">Linux ARM</button>
        <button type="button" data-kx-platform="darwin_arm64" aria-pressed="false">macOS Apple silicon</button>
        <button type="button" data-kx-platform="darwin_amd64" aria-pressed="false">macOS Intel</button>
      </div>
      <div class="kx-install__command">curl -sSL https://github.com/<wbr>jzills/<wbr>kx/<wbr>releases/<wbr>latest/<wbr>download/<wbr><span data-kx-asset>kx_linux_amd64</span>.tar.gz | tar xz</div>
      <div class="kx-install__command">sudo install kx/kx /usr/local/bin/kx</div>
      <p class="kx-install__note">Windows builds and checksums are on <a href="https://github.com/jzills/kx/releases/latest">the latest release</a>.</p>
      <span class="kx-install__status" role="status"></span>
    </div>
    <div class="kx-install__card" data-kx-copy>
      <div class="kx-install__head">
        <div class="kx-install__label">pipx</div>
        <button type="button" class="kx-install__copy" aria-label="Copy the pipx install command" hidden><svg viewBox="0 0 16 16" aria-hidden="true" class="kx-install__icon kx-install__icon--copy"><rect x="5" y="5" width="9" height="9" rx="1.5"/><path d="M11 5V3.5A1.5 1.5 0 0 0 9.5 2h-6A1.5 1.5 0 0 0 2 3.5v6A1.5 1.5 0 0 0 3.5 11H5"/></svg><svg viewBox="0 0 16 16" aria-hidden="true" class="kx-install__icon kx-install__icon--done"><path d="M3 8.5 6.5 12 13 4.5"/></svg></button>
      </div>
      <div class="kx-install__command">pipx install kx-cli</div>
      <span class="kx-install__status" role="status"></span>
    </div>
    <div class="kx-install__card" data-kx-copy>
      <div class="kx-install__head">
        <div class="kx-install__label">Try it without installing</div>
        <button type="button" class="kx-install__copy" aria-label="Copy the command to try kx without installing" hidden><svg viewBox="0 0 16 16" aria-hidden="true" class="kx-install__icon kx-install__icon--copy"><rect x="5" y="5" width="9" height="9" rx="1.5"/><path d="M11 5V3.5A1.5 1.5 0 0 0 9.5 2h-6A1.5 1.5 0 0 0 2 3.5v6A1.5 1.5 0 0 0 3.5 11H5"/></svg><svg viewBox="0 0 16 16" aria-hidden="true" class="kx-install__icon kx-install__icon--done"><path d="M3 8.5 6.5 12 13 4.5"/></svg></button>
      </div>
      <div class="kx-install__command">uvx --from kx-cli kx get pods</div>
      <span class="kx-install__status" role="status"></span>
    </div>
  </div>
</div>

<div class="kx-section" id="commands">
  <h2 class="kx-section__title">One listing, every command</h2>
  <p class="kx-section__lede">
    Indexes come from the last listing and stay put. Several at once, a
    range, or a range left open — <code>kx delete 1 2..4 5..</code>
  </p>
{{< kx-terminal >}}
</div>

<div class="kx-section" id="features">
  <h2 class="kx-section__title">More than a shorter kubectl</h2>
  <p class="kx-section__lede">
    Each of these has a guide in <a href="docs/">the documentation</a>.
  </p>
  <div class="kx-features">
    <div class="kx-feature">
      <p class="kx-feature__title"><a href="docs/guides/triage-a-namespace/">Triage a namespace</a></p>
      <p class="kx-feature__body">
        Bare <code>kx diag</code> sweeps every workload and ranks what is unhealthy —
        CrashLoopBackOff, image pull failures, OOMKill risk read from live
        usage, stalled rollouts, Services with no endpoints. The rows are
        indexed, so you drill straight in.
      </p>
    </div>
    <div class="kx-feature">
      <p class="kx-feature__title"><a href="docs/guides/scan-images/">Scan images for CVEs</a></p>
      <p class="kx-feature__body">
        The <code>kx scan</code> command resolves a workload's unique images and
        scans each one, printing a severity summary. Docker Scout by default,
        Trivy or Grype with <code>--engine</code>.
      </p>
    </div>
    <div class="kx-feature">
      <p class="kx-feature__title"><a href="docs/guides/read-a-secret/">Read a Secret in plaintext</a></p>
      <p class="kx-feature__body">
        Running <code>kx secret 1 --decode</code> prints keys and values decoded, with
        binary payloads shown as a placeholder rather than garbling the table.
        With <code>-k</code>, one value prints raw, straight into a shell.
      </p>
    </div>
    <div class="kx-feature">
      <p class="kx-feature__title"><a href="docs/guides/ownership-tree/">Ownership, as a tree</a></p>
      <p class="kx-feature__body">
        The <code>kx tree</code> command walks ownership references from controllers down to
        containers — the structure kubectl's table output cannot show. Indexed
        like every other listing.
      </p>
    </div>
    <div class="kx-feature">
      <p class="kx-feature__title"><a href="docs/guides/browser-reports/">Reports in the browser</a></p>
      <p class="kx-feature__body">
        The <code>--html</code> flag on diag, scan, tree and top renders the same
        analysis as a filterable page and opens it. Bound to localhost, nothing
        written to disk, no extra API calls.
      </p>
    </div>
    <div class="kx-feature">
      <p class="kx-feature__title"><a href="docs/concepts/completion/">Completion that knows your listing</a></p>
      <p class="kx-feature__body">
        Typing <code>kx describe &lt;TAB&gt;</code> offers <code>1  api-7d8f (Pod)</code>,
        not a bare number. Answered from saved state, so it never waits on the
        cluster.
      </p>
    </div>
  </div>
</div>

<div class="kx-section" id="themes">
  <h2 class="kx-section__title">In your colors</h2>
  <p class="kx-section__lede">
    The <code>kx theme</code> command restyles the terminal, the HTML reports — and this
    page. Same palettes, one registry. Pick one:
  </p>

  {{< kx-themes >}}
</div>

<div class="kx-section" id="reports">
  <h2 class="kx-section__title">Browser reports</h2>
  <p class="kx-section__lede">
    The <code>--html</code> flag renders the same analysis as a page and opens it. Sweep
    rows expand into a resource's full report; image rows expand into the CVEs
    behind their counts. Bound to localhost, nothing written to disk.
  </p>
  {{< kx-shot report="diag" alt="kx diag --html dashboard" >}}
</div>

<div class="kx-section">
  <h3 class="kx-section__title">kx scan --html</h3>
  <p class="kx-section__lede">
    Per-image severity counts, with the CVE table grouped by image below.
  </p>
  {{< kx-shot report="scan" alt="kx scan --html dashboard" >}}
</div>

<div class="kx-section">
  <h3 class="kx-section__title">kx tree --html</h3>
  <p class="kx-section__lede">
    The ownership graph as a collapsible tree, indexed like every other listing.
  </p>
  {{< kx-shot report="tree" alt="kx tree --html dashboard" >}}
</div>
