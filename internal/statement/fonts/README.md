# NotoSansSC-Statement.ttf

The CJK face the usage-statement PDF renders with. Embedded into the binary by
`//go:embed` from `internal/statement/font.go`, so its size is the binary's size
— which is why it is a subset and not the 19 MB upstream collection.

- **Upstream**: Noto Sans CJK SC Regular, face index 2 of
  `NotoSansCJK-Regular.ttc` (Arch package `noto-fonts-cjk`).
- **Licence**: SIL Open Font License 1.1 — see `LICENSE-NotoSansCJK.txt`. The
  OFL permits embedding and redistribution; it forbids selling the font on its
  own and requires the licence travel with the file, which is what that file is
  doing here. Reserved Font Name rules are why the output is *not* named
  "Noto Sans CJK SC" — see the `--name-IDs` note below.
- **Size**: 2.1 MB, from 7,546 glyphs.

## Why it is built the way it is

Two transformations, each forced by a downstream constraint:

**Subset to GB2312.** The full SC face is ~10 MB. The statement's fixed text is
maybe 150 distinct characters, but the *variable* text is not: a token label or
a workspace name can be any Chinese a customer typed. GB2312 (level-1 + level-2
+ symbol rows, 6,763 hanzi) is the coverage line that makes an out-of-subset
character effectively impossible in real Chinese input, which matters because a
missing glyph on a document someone submits for reimbursement renders as blank —
silently, with no error anywhere.

**CFF → TrueType `glyf`.** Noto Sans CJK ships CFF (`OTTO`) outlines. Every
mainstream Go PDF library — gopdf, fpdf, maroto — parses TrueType `glyf` only,
so the cubic curves are approximated by quadratics via `cu2qu` at a 1/1000-em
error bound. This is where the file grows from 1.5 MB to 2.1 MB; quadratics need
more points to say the same thing.

## Regenerating

Needs `fonttools` (no system install required — `uv` fetches it into a temp
environment) and the upstream `.ttc` on disk.

```sh
cd internal/statement/fonts
uv tool run --from fonttools python mkcharset.py          # writes charset.txt
uv tool run --from fonttools pyftsubset \
  /usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc \
  --font-number=2 --text-file=charset.txt \
  --output-file=/tmp/subset.otf \
  --layout-features='' --no-hinting --desubroutinize \
  --name-IDs='0,1,2,3,4,5,6' --drop-tables+=DSIG --recalc-bounds
uv tool run --from fonttools python otf2ttf.py /tmp/subset.otf NotoSansSC-Statement.ttf
```

`charset.txt` is generated, not checked in. `--name-IDs` keeps the name table so
the licence and designer strings survive into the PDF's embedded copy.

If you widen the charset, re-check the binary size before tagging: this file is
`//go:embed`-ed, so every byte here is a byte in every release artifact for
every platform.
