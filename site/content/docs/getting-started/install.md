---
title: Install
description: krew, uv, pipx, pip, or a standalone binary — all the same build.
weight: 1
---

The only requirement is `kubectl` on your `PATH`.
Every method below installs the same prebuilt Go binary.

## As a kubectl plugin

On [krew](https://krew.sigs.k8s.io/), kx is published under the name `idx`.

```bash
kubectl krew install idx
alias kx="kubectl idx"
```

The alias is worth setting: every example in these docs is written as `kx`, and `kubectl idx describe 2` is a long way to say `kx describe 2`.

## With uv

From PyPI, [uv](https://docs.astral.sh/uv/) puts the binary on your `PATH` in its own environment and keeps it out of everything else — no Python runtime, no dependencies, no compiler.
The package is called `kx-cli`; the command it installs is `kx`.

```bash
uv tool install kx-cli
```

## Standalone binaries

Builds for Linux, macOS and Windows on both amd64 and arm64 are attached to every [GitHub Release](https://github.com/jzills/kx/releases), with checksums in `SHA256SUMS`.
Download, verify, and drop the binary somewhere on your `PATH`.

The newest build is always at the same URL, so on Linux or macOS installing it is one command.
Swap `linux_amd64` for `linux_arm64`, `darwin_arm64` or `darwin_amd64` to match your machine:

```bash
curl -sSL \
  https://github.com/jzills/kx/releases/latest/download/kx_linux_amd64.tar.gz \
  | tar xz
sudo install kx/kx /usr/local/bin/kx
```

## With pipx or pip

The same PyPI package installs with either:

```bash
pipx install kx-cli
pip install kx-cli
```

## Without installing anything

```bash
uvx --from kx-cli kx get pods
pipx run --spec kx-cli kx get pods
```

Both fetch the package, run it, and leave nothing behind.
Handy on a machine you don't own — though the saved listing still lands in `~/.kx/`, so the index workflow works across those invocations too.

## Verify it

```bash
kx --version
```

That prints the version, the commit it was built from, the Go toolchain and platform, and the paths kx reads its config and state from.

{{% kx-note kind="warn" %}}
On macOS, the first run of a freshly installed krew plugin or standalone binary takes a few seconds while Gatekeeper scans it.
Later runs are unaffected until the next install.
Getting it over with up front — `kx --version >/dev/null` — is nicer than discovering it mid-incident.
{{% /kx-note %}}

## Shell completion

Tab completion shows the resource behind each index, so `kx describe <TAB>` offers `1  api-7d8f (Pod)` rather than a bare number.
See [completion](../../concepts/completion/) for the per-shell setup.

## Next

[Quickstart](../quickstart/) walks through the first listing and what you can do with it.
