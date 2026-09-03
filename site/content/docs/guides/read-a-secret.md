---
title: Read a Secret in plaintext
description: kx secret --decode, and -k for a single value that drops straight into a shell.
weight: 3
---

kubectl returns Secret data base64-encoded, so reading one means a pipe through
`base64 -d` and some `jsonpath`.

```bash
kx secret            # list Secrets, indexed like any other listing
kx secret 1 --decode # keys and values, decoded
```

Values that aren't text show as `<binary, N bytes>` rather than garbling the
table with control characters.

## One value, raw

```bash
kx secret 1 --decode -k password
```

`-k`/`--key` prints a single value with no banner, no table and no trailing
newline fuss — so it composes:

```bash
export PGPASSWORD=$(kx secret 1 --decode -k password)
kx secret 1 --decode -k tls.crt > tls.crt
```

That second one is why binary values are supported rather than refused:
redirecting to a file is the sensible thing to do with a certificate or a
keystore.

## The whole namespace

```bash
kx secret --decode
```

decodes every Secret in the namespace in one call. It asks for confirmation
first, because that prints every credential in the namespace to your terminal
and into your scrollback. `--yes`/`-y` skips the prompt, for when you meant it.

{{% kx-note kind="warn" %}}
Decoded output is plaintext in your terminal buffer, and `export` puts it in
your shell history in some configurations. `-k` into a variable or a file is
the narrower habit.
{{% /kx-note %}}

## Elsewhere

`-n` and `-A` work as they do everywhere:

```bash
kx secret -n prod
kx secret -A
```

`kx secrets` is an alias, and `kx get secrets --decode` is the same thing
spelled the long way — `--decode` lives on [`kx get`](../../reference/commands/get/)
too.
