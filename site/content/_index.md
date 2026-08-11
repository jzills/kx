---
title: "kx — kubectl, indexed"
toc: false
---

<div class="kx-hero">
  <img class="kx-hero__logo" src="img/banner.svg" alt="kx" />
  <p class="kx-hero__tagline">kubectl, indexed.</p>
  <p class="kx-hero__blurb">
    Run <code>kx get pods</code> once and every row gets a number. From then on
    you reference resources by that number instead of typing names —
    <code>kx logs 3</code>, <code>kx delete 2 5</code>, <code>kx exec 1</code>.
  </p>
</div>

<div class="kx-section">
  <img class="kx-media" src="demo/demo.gif" alt="Listing pods with kx and acting on them by index" loading="lazy" decoding="async" width="800" />
</div>

<div class="kx-section" id="install">
  <h2 class="kx-section__title">Install</h2>
  <p class="kx-section__lede">
    Requires <code>kubectl</code> on your PATH. Every path below delivers the
    same prebuilt binary — no Python runtime, no dependencies.
  </p>
  <div class="kx-install">
    <div class="kx-install__card">
      <div class="kx-install__label">uv</div>
      <div class="kx-install__command">uv tool install kx-cli</div>
    </div>
    <div class="kx-install__card">
      <div class="kx-install__label">pipx</div>
      <div class="kx-install__command">pipx install kx-cli</div>
    </div>
    <div class="kx-install__card">
      <div class="kx-install__label">krew</div>
      <div class="kx-install__command">kubectl krew install idx</div>
    </div>
    <div class="kx-install__card">
      <div class="kx-install__label">Try it without installing</div>
      <div class="kx-install__command">uvx --from kx-cli kx get pods</div>
    </div>
  </div>
</div>

<div class="kx-section" id="commands">
  <h2 class="kx-section__title">One listing, every command</h2>
  <p class="kx-section__lede">
    Indexes come from the last listing and stay put. Several at once, or a
    range — either end can be left open.
  </p>
  <div class="kx-terminal">
<pre><span class="c-muted">$</span> kx get pods
  <span class="c-accent">X</span>  <span class="c-accent">NAME</span>                     <span class="c-accent">READY</span>   <span class="c-accent">STATUS</span>             <span class="c-accent">AGE</span>
  1  api-7d8f4c6b9-2xk4l      1/1     <span class="c-ok">Running</span>            4d
  2  web-5c9f8d7a4-mn3pq      1/1     <span class="c-ok">Running</span>            4d
  3  worker-6b8d9f2c1-qr7st   0/1     <span class="c-bad">CrashLoopBackOff</span>   12m
  4  cache-9f3a1b8e5-vw2xy    1/1     <span class="c-ok">Running</span>            9d

<span class="c-muted">$</span> kx logs 3 <span class="c-muted">--tail=50</span>
<span class="c-muted">$</span> kx delete 2 4
<span class="c-muted">$</span> kx describe 1..3
<span class="c-muted">$</span> kx diag</pre>
  </div>
</div>

<div class="kx-section">
  <h2 class="kx-section__title">More than a shorter kubectl</h2>
  <div class="kx-features">
    <div class="kx-feature">
      <p class="kx-feature__title">Triage a namespace</p>
      <p class="kx-feature__body">
        <code>kx diag</code> sweeps every workload and ranks what is unhealthy —
        CrashLoopBackOff, image pull failures, OOMKill risk read from live
        usage, stalled rollouts, Services with no endpoints. The rows are
        indexed, so you drill straight in.
      </p>
    </div>
    <div class="kx-feature">
      <p class="kx-feature__title">Scan images for CVEs</p>
      <p class="kx-feature__body">
        <code>kx scan</code> resolves a workload's unique images and scans each
        one, printing a severity summary. Docker Scout by default, Trivy with
        <code>--engine trivy</code>.
      </p>
    </div>
    <div class="kx-feature">
      <p class="kx-feature__title">Read a Secret in plaintext</p>
      <p class="kx-feature__body">
        <code>kx secret 1 --decode</code> prints keys and values decoded, with
        binary payloads shown as a placeholder rather than garbling the table.
        <code>-k</code> prints one value raw, straight into a shell.
      </p>
    </div>
    <div class="kx-feature">
      <p class="kx-feature__title">Ownership, as a tree</p>
      <p class="kx-feature__body">
        <code>kx tree</code> walks ownership references from controllers down to
        containers — the structure kubectl's table output cannot show. Indexed
        like every other listing.
      </p>
    </div>
    <div class="kx-feature">
      <p class="kx-feature__title">Reports in the browser</p>
      <p class="kx-feature__body">
        <code>--html</code> on diag, scan, tree and top renders the same
        analysis as a filterable page and opens it. Bound to localhost, nothing
        written to disk, no extra API calls.
      </p>
    </div>
    <div class="kx-feature">
      <p class="kx-feature__title">Completion that knows your listing</p>
      <p class="kx-feature__body">
        <code>kx describe &lt;TAB&gt;</code> offers <code>1  api-7d8f (Pod)</code>,
        not a bare number. Answered from saved state, so it never waits on the
        cluster.
      </p>
    </div>
  </div>
</div>

<div class="kx-section">
  <h2 class="kx-section__title">In your colours</h2>
  <p class="kx-section__lede">
    <code>kx theme</code> restyles the terminal, the HTML reports — and this
    page. Same palettes, one registry. Pick one:
  </p>

  {{< kx-themes >}}
</div>

<div class="kx-section">
  <h2 class="kx-section__title">Browser reports</h2>
  <p class="kx-section__lede">
    Sweep rows expand into a resource's full report; image rows expand into the
    CVEs behind their counts.
  </p>
  <img class="kx-media" src="img/diag-html.png" alt="kx diag --html dashboard" loading="lazy" decoding="async" width="800" />
</div>
