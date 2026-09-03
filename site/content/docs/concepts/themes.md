---
title: Themes
description: Ten palettes for the terminal, the HTML reports, and this page.
weight: 4
---

```bash
kx theme              # list the palettes, with a preview of each
kx theme dracula      # persist a choice
kx theme 3            # ...or pick it by number, like anything else
```

The choice is written to [`~/.kx/config.toml`](../configuration/), and
`KX_THEME` overrides it for a single shell.

## The palettes

`github-dark` (the default), `dracula`, `nord`, `gruvbox`, `solarized-dark`,
`catppuccin-mocha`, `tokyo-night`, `rose-pine`, `mono`, `light`, and `plain`.

`light` is for light terminal backgrounds. `mono` drops colour but keeps bold
and dim, for terminals or captures where hue is noise. `plain` disables
styling altogether, the same as `--no-color`.

{{% kx-note %}}
A palette is a set of colours, not a background. On a terminal whose own
background disagrees with the one a palette assumes, the palette will look
wrong — that is the terminal and the palette disagreeing, not a bug in either.
`light` exists for exactly this reason.
{{% /kx-note %}}

## One registry, three surfaces

The palettes are defined once, in the `kx` binary. Three things read them:

- **the terminal**, for every command's output
- **the HTML reports** — `kx diag --html` and friends are drawn in the palette
  you have set, so `kx theme nord` restyles them too
- **this site**, whose stylesheet is generated from the same registry

Which is why the picker in the navbar offers the same ten names `kx theme`
prints. Pick one and the page follows, tab icon included.

{{< kx-themes >}}
