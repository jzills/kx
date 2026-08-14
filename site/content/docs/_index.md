---
title: Documentation
linkTitle: Docs
description: Install kx, learn the index workflow, and look up any command.
weight: 1
---

`kx` wraps kubectl and gives every row of a listing a number. Run `kx get
pods` once and the pods are 1, 2, 3; from then on every other command takes
those numbers instead of a name you have to read off the screen and retype.

```bash
kx get pods          # 1  api-7d8f...   2  billing-5c4b...   3  cache-9a1e...
kx logs 2
kx describe 1 3
kx delete 3..5
```

Nothing else changes: flags you already know pass through to kubectl, the
output is kubectl's own table with an index column in front, and every
command is one you can still run by hand.

{{< kx-children >}}
