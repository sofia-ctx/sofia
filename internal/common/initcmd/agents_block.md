<!-- sf:begin -->
## Source context via `sf`

Before reading or searching source files (.go/.php/.ts/.tsx/.vue), prefer the `sf` CLI — structural context at a fraction of the tokens:

- File shape (types, signatures, API): `sf code <file1> [file2 ...]` — batch several files into ONE call; small files come back raw automatically. Or a directory for a whole-package map: `sf code <dir> --brief`.
- Symbol bodies: `sf code <file> <Sym1> [Sym2 ...]` — several bodies in one call.
- Tree search: `sf grep --ext=go,php '<pattern>' [more patterns]` — capped hits, instead of `grep -rn`.
- Find a symbol's definition and callers: `sf refs <symbol>` — one call, each hit labeled with its enclosing function.
- What changed: `sf changed [range]` — instead of a full `git diff`.

Rules:
- Batch what you need NOW into one call; don't pre-load for later.
- A file you are about to EDIT: read it natively — sf is for understanding, not for edit targets.
- Repeating an identical `sf code` call within minutes returns a short "already returned" stub — reuse the earlier output, or rerun with `--force` if you truly need it again.
- Output ends with a cost footer (`# sf ≈N tok · raw ≈M · saved ≈K`); trust it, don't re-read raw to verify.
<!-- sf:end -->
