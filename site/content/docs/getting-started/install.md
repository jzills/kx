---
title: Install
description: uv, pipx, pip, krew, or a standalone binary — all the same build.
weight: 1
---

`kx` needs `kubectl` on your `PATH` and nothing else. Every path below
delivers the same prebuilt Go binary: no Python runtime, no dependencies, no
compiler.

The package is called `kx-cli`; the command it installs is `kx`.

## With uv

[uv](https://docs.astral.sh/uv/) is the recommended path — it puts the binary
on your `PATH` in its own environment and keeps it out of everything else.

```bash
uv tool install kx-cli
```

## With pipx

```bash
pipx install kx-cli
```

## With pip

```bash
pip install kx-cli
```

## As a kubectl plugin

`kx` is published to [krew](https://krew.sigs.k8s.io/) as `idx` — `kx` was
already taken in the plugin index.

```bash
kubectl krew install idx
alias kx="kubectl idx"
```

The alias is worth setting: every example in these docs is written as `kx`,
and `kubectl idx describe 2` is a long way to say `kx describe 2`.

## Standalone binaries

Builds for Linux, macOS and Windows on both amd64 and arm64 are attached to
every [GitHub Release](https://github.com/jzills/kx/releases), with checksums
in `SHA256SUMS`. Download, verify, and drop the binary somewhere on your
`PATH`.

## Without installing anything

```bash
uvx --from kx-cli kx get pods
pipx run --spec kx-cli kx get pods
```

Both fetch the package, run it, and leave nothing behind. Handy on a machine
you don't own — though the saved listing still lands in `~/.kx/`, so the
index workflow works across those invocations too.

## Verify it

```bash
kx --version
```

That prints the version, the commit it was built from, the Go toolchain and
platform, and the paths `kx` reads its config and state from.

{{% kx-note kind="warn" %}}
On macOS, the first run of a freshly installed krew plugin or standalone
binary takes a few seconds while Gatekeeper scans it. Later runs are
unaffected until the next install. Getting it over with up front —
`kx --version >/dev/null` — is nicer than discovering it mid-incident.
{{% /kx-note %}}

## Shell completion

`kx` completes indexes with the resource each one points at, so
`kx describe <TAB>` offers `1  api-7d8f (Pod)` rather than a bare number.
See [completion](../../concepts/completion/) for the per-shell setup.

## Next

[Quickstart](../quickstart/) walks through the first listing and what you can
do with it.
