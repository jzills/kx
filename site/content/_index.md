---
title: "kx — kubectl, indexed"
toc: false
---

<!--
  The landing page: what kx is, one command to install it, the listing it
  numbers, and what it does beyond that, each linking into the docs. The words
  that need saying at length live in docs/; this page is the pitch.

  Every color is a --kx-* property from the active palette, so the palette
  menu restyles this page as it does the docs.
-->

<section class="kx-hero">
  <span class="kx-hero__logo">{{< kx-mark >}}</span>
  <h1 class="kx-hero__title">kubectl, indexed.</h1>
  <p class="kx-hero__lede">
    Run <code>kx get &lt;resource&gt;</code> once and every row gets a number.
    From then on you name resources by that number instead of typing them out:
    <code>kx logs 2</code>, <code>kx delete 2 5</code>, <code>kx exec 1</code>.
  </p>
  <div class="kx-hero__actions">
    <a class="kx-button kx-button--primary" href="docs/getting-started/quickstart/">Get started</a>
    <a class="kx-button" href="docs/">Documentation</a>
  </div>
  <div class="kx-command" data-kx-copy>
    <code class="kx-install__command">curl -fsSL https://jzills.github.io/kx/install.sh | sh</code>
    <button type="button" class="kx-install__copy" aria-label="Copy the install script command" hidden><svg viewBox="0 0 16 16" aria-hidden="true" class="kx-install__icon kx-install__icon--copy"><rect x="5" y="5" width="9" height="9" rx="1.5"/><path d="M11 5V3.5A1.5 1.5 0 0 0 9.5 2h-6A1.5 1.5 0 0 0 2 3.5v6A1.5 1.5 0 0 0 3.5 11H5"/></svg><svg viewBox="0 0 16 16" aria-hidden="true" class="kx-install__icon kx-install__icon--done"><path d="M3 8.5 6.5 12 13 4.5"/></svg></button>
    <span class="kx-install__status" role="status"></span>
  </div>
  <p class="kx-hero__alt">
    It runs on Linux, macOS and Windows, beside the <code>kubectl</code> you already have.
    <a href="#install">Other ways to install</a>
  </p>
</section>

<figure class="kx-window kx-window--hero">
  <div class="kx-window__bar" aria-hidden="true"><span></span><span></span><span></span></div>
{{< kx-hero-terminal >}}
</figure>

<section class="kx-features" id="features" aria-label="What kx does">
  <div class="kx-feature">
    <h2 class="kx-feature__title">Triage a namespace</h2>
    <p>Bare <code>kx diag</code> sweeps every workload and ranks what is unhealthy: CrashLoopBackOff, image pull failures, OOMKill risk, stalled rollouts and Services with no endpoints.</p>
    <a href="docs/guides/triage-a-namespace/">Triage a namespace</a>
  </div>
  <div class="kx-feature">
    <h2 class="kx-feature__title">Scan images for CVEs</h2>
    <p>The <code>kx scan</code> command resolves a workload's unique images and scans each one with Docker Scout, Trivy or Grype, then prints a severity summary.</p>
    <a href="docs/guides/scan-images/">Scan images</a>
  </div>
  <div class="kx-feature">
    <h2 class="kx-feature__title">Ownership, as a tree</h2>
    <p>The <code>kx tree</code> command walks ownership references from controllers down to containers, the structure kubectl's table output cannot show, and numbers it like any other listing.</p>
    <a href="docs/guides/ownership-tree/">Ownership trees</a>
  </div>
  <div class="kx-feature">
    <h2 class="kx-feature__title">Read a Secret in plaintext</h2>
    <p>Running <code>kx secret 1 --decode</code> prints keys and values decoded, with binary payloads shown as a placeholder. With <code>-k</code>, one value prints raw, straight into a shell.</p>
    <a href="docs/guides/read-a-secret/">Read a Secret</a>
  </div>
  <div class="kx-feature">
    <h2 class="kx-feature__title">Completion that knows your listing</h2>
    <p>Typing <code>kx describe &lt;TAB&gt;</code> offers <code>1&nbsp;&nbsp;api-7d8f (Pod)</code>, not a bare number. It answers from saved state, so it never waits on the cluster.</p>
    <a href="docs/concepts/completion/">Completion</a>
  </div>
  <div class="kx-feature">
    <h2 class="kx-feature__title">Built for agents and CI</h2>
    <p>The <code>kx mcp</code> server lets an agent read the cluster without writing to it, and <code>--json</code> with <code>--fail-on</code> turns a sweep or a scan into a pipeline gate.</p>
    <a href="docs/guides/use-kx-from-an-agent/">Use kx from an agent</a>
  </div>
</section>

<section class="kx-showcase" id="commands">
  <div class="kx-showcase__text">
    <h2>One listing, every command</h2>
    <p>
      Indexes come from the last listing and stay put until you list again.
      Name several at once, a range, or a range left open, as in <code>kx delete 1 2..4 5..</code>, and <code>--sort</code> and <code>--limit</code> decide which rows get the first numbers.
    </p>
    <a href="docs/concepts/indexes/">How indexes work</a>
  </div>
  <figure class="kx-window">
    <div class="kx-window__bar" aria-hidden="true"><span></span><span></span><span></span></div>
{{< kx-terminal >}}
  </figure>
</section>

<section class="kx-showcase" id="reports">
  <div class="kx-showcase__text">
    <h2>Reports in the browser</h2>
    <p>
      The <code>--html</code> flag on <code>diag</code>, <code>scan</code>, <code>tree</code> and <code>top</code> renders the same analysis as a page you can filter and opens it.
      Sweep rows expand into a resource's full report, and image rows into the CVEs behind their counts.
      It's served from localhost, and nothing is written to disk unless you pass <code>--out</code>.
    </p>
    <a href="docs/guides/browser-reports/">Browser reports</a>
  </div>
  <figure class="kx-shot kx-shot--wide">
    {{< kx-shot report="diag" alt="kx diag --html: a namespace sweep, ranked by severity" >}}
    <figcaption>kx diag --html</figcaption>
  </figure>
  <div class="kx-shots">
    <figure class="kx-shot">
      {{< kx-shot report="scan" alt="kx scan --html: severity counts per image" >}}
      <figcaption>kx scan --html</figcaption>
    </figure>
    <figure class="kx-shot">
      {{< kx-shot report="tree" alt="kx tree --html: the ownership graph as a collapsible tree" >}}
      <figcaption>kx tree --html</figcaption>
    </figure>
  </div>
</section>

<section class="kx-split" id="themes">
  <div class="kx-split__text">
    <h2>In your colors</h2>
    <p>
      The <code>kx theme</code> command restyles the terminal, the HTML reports and this page from one registry of palettes.
      Pick one here and the whole site follows.
    </p>
    <a href="docs/concepts/themes/">Themes</a>
  </div>
  {{< kx-themes >}}
</section>

<section class="kx-closing" id="install">
  <h2>Install kx</h2>
  <p>Every method installs the same prebuilt binary, from PyPI too, with no Python runtime. It needs only <code>kubectl</code> on your PATH.</p>
  <div class="kx-install">
    <div class="kx-install__card" data-kx-copy>
      <div class="kx-install__head">
        <div class="kx-install__label">Homebrew</div>
        <button type="button" class="kx-install__copy" aria-label="Copy the Homebrew install command" hidden><svg viewBox="0 0 16 16" aria-hidden="true" class="kx-install__icon kx-install__icon--copy"><rect x="5" y="5" width="9" height="9" rx="1.5"/><path d="M11 5V3.5A1.5 1.5 0 0 0 9.5 2h-6A1.5 1.5 0 0 0 2 3.5v6A1.5 1.5 0 0 0 3.5 11H5"/></svg><svg viewBox="0 0 16 16" aria-hidden="true" class="kx-install__icon kx-install__icon--done"><path d="M3 8.5 6.5 12 13 4.5"/></svg></button>
      </div>
      <div class="kx-install__command">brew install jzills/tap/kx</div>
      <span class="kx-install__status" role="status"></span>
    </div>
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
    <div class="kx-install__card" data-kx-copy>
      <div class="kx-install__head">
        <div class="kx-install__label">pipx</div>
        <button type="button" class="kx-install__copy" aria-label="Copy the pipx install command" hidden><svg viewBox="0 0 16 16" aria-hidden="true" class="kx-install__icon kx-install__icon--copy"><rect x="5" y="5" width="9" height="9" rx="1.5"/><path d="M11 5V3.5A1.5 1.5 0 0 0 9.5 2h-6A1.5 1.5 0 0 0 2 3.5v6A1.5 1.5 0 0 0 3.5 11H5"/></svg><svg viewBox="0 0 16 16" aria-hidden="true" class="kx-install__icon kx-install__icon--done"><path d="M3 8.5 6.5 12 13 4.5"/></svg></button>
      </div>
      <div class="kx-install__command">pipx install kx-cli</div>
      <span class="kx-install__status" role="status"></span>
    </div>
  </div>
  <p class="kx-closing__try">To try it without installing, run <code>uvx --from kx-cli kx get pods</code>.</p>
  <div class="kx-hero__actions">
    <a class="kx-button kx-button--primary" href="docs/getting-started/install/">Installation guide</a>
    <a class="kx-button" href="https://github.com/jzills/kx">GitHub</a>
  </div>
</section>

