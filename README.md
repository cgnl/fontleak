# fontleak

Did you become third in a CTF due to solving a TTF font riddle manually? Are you
frustrated that no existing tools exist for this? Well now there is one. Enjoy.

`fontleak` finds hidden data and CTF-style *logic* in fonts — and in the
documents that embed them. It deobfuscates embedded fonts, repairs tampered
font tables, summarises the GSUB "program", and automatically tackles the two
font-puzzle archetypes:

- **Checker fonts** — the font validates a typed string and only renders a
  readable success phrase (e.g. *"Your Flag Is Correct"*) for the right input.
  fontleak finds the recognition rule, extracts the expected glyph sequence and
  the success phrase, and (for tractable ciphers) recovers the secret by
  black-box inversion. It can always **verify** a candidate via the font's own
  self-check.
- **Ligature-collapse fonts** — the font chains typed characters into ligatures
  that collapse to a single glyph. fontleak reverses the ligature/substitution
  chains to recover the hidden input string.

Pure Go, no cgo. Built on [`go-text/typesetting`](https://github.com/go-text/typesetting)
(a pure-Go HarfBuzz port + OpenType parser).

## Install

```
go build -o fontleak .
```

## Usage

```
fontleak scan    <file>                 extract → normalize → inspect → solve, full report
fontleak extract <doc> [-o dir]         dump deobfuscated embedded fonts
fontleak inspect <font>                 static analysis (strings, GSUB, homoglyphs)
fontleak shape   <font> "text"          forward shaping oracle (input → glyphs)
fontleak solve   <font> [-mode auto|checker|ligature] [-alphabet 0123456789abcdef]
fontleak verify  <font> "PVIB{...}"     test whether the font accepts an input
```

`<file>` may be a loose `.ttf`/`.otf` **or** an OOXML document (`.docx`/`.pptx`/
`.xlsx`) — embedded fonts are unzipped and de-obfuscated automatically. When a
document holds several fonts, the logic-bearing one (most GSUB lookups) is used.

## What it does, stage by stage

1. **Extract** — unzips OOXML, reads `fontTable.xml` + relationships, and
   de-obfuscates each embedded `.odttf` (Microsoft's GUID-keyed XOR). Empty
   references (a common red herring) are detected and skipped.
2. **Normalize** — repairs sfnt directories where layout tables were renamed
   (e.g. `GSUB` → `ykm|`) and their lengths falsified, so any parser can read
   them. (Safe on already-valid fonts.)
3. **Inspect** — embedded strings, flag-shaped substrings, a GSUB lookup
   summary, and code points whose glyph outlines are identical (confusables).
4. **Solve** — detects checker / ligature designs and recovers or verifies the
   secret.

## Example: NEW_COASTER (TFCCTF 2025)

```
$ fontleak scan NEW_COASTER.docx
...
── Theme Park Sans™ [Regular] (...!word/fonts/font11.odttf)
   GSUB: 333 lookups, 17249 ligatures map[ChainedContextualSubst:88 ...]
   • GSUB contains 333 lookups — far more than normal typography; this font likely encodes logic.
   CHECKER detected: input PVIB{<secret>} renders "Your Flag Is Correct" (secret length 44)
   ...

$ fontleak verify NEW_COASTER.docx "PVIB{4665697374656c20466f6e743a205468652052696465}"
renders:  "Your Flag Is Correct"
result:   ✓ ACCEPTED
```

The hex decodes to ASCII *"Feistel Font: The Ride"* — a theme-park pun on the
Feistel cipher the font implements.

## Scope & limits (honest)

- Extraction, normalization, inspection, checker/ligature **detection**, target
  extraction and **verification** are fully generic.
- **Blind secret recovery** works for position-local and triangular/small-seed
  ciphers (a common class). A *strong, full-diffusion* cipher (like
  NEW_COASTER's multi-round Feistel) cannot be inverted by black-box search with
  only an encrypt oracle — that's the whole point of a good cipher. fontleak
  detects it, extracts the target glyph sequence and success phrase, and lets
  you `verify` candidates; finishing such a cipher still needs font-specific
  cryptanalysis. (That's the part you solved by hand. Sorry.)

## Layout

```
extract/   OOXML + .odttf deobfuscation
sfnt/      table-directory normalization (rename/length repair)
gsub/      GSUB rule extraction (single/multiple/ligature + glyph↔char)
oracle/    HarfBuzz forward-shaping wrapper
inspect/   static analysis (strings, regex, lookup stats, homoglyphs)
solve/     checker inversion + ligature expansion
report/    scan pipeline
main.go    CLI
```

## Tests

```
go test ./...
```

Tests run against the NEW_COASTER challenge artifacts when present (byte-exact
deobfuscation vs the reference, normalization round-trip, shaping parity, checker
detection + verification, and blind inversion of a synthetic tractable cipher).
