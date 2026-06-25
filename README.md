# fontleak

Did you become third in a CTF due to solving a TTF font riddle manually? Are you
frustrated that no existing tools exist for this? Well now there is one. Enjoy.

`fontleak` finds hidden data and CTF-style *logic* in fonts, and in the documents
that embed them. It deobfuscates embedded fonts, repairs tampered font tables,
summarises the GSUB "program", and automatically solves the two font-puzzle
archetypes:

* **Checker fonts.** The font validates a typed string and only renders a
  readable success phrase (for example *"Your Flag Is Correct"*) for the right
  input. fontleak finds the recognition rule, reads the glyph sequence it
  expects, identifies the success phrase, and recovers the secret.
* **Ligature-collapse fonts.** The font chains typed characters into ligatures
  that collapse to a single glyph. fontleak reverses the ligature chains to
  recover the hidden input strings, then auto-selects the real flag: the correct
  input collapses to a glyph that *draws a readable letter* (the success signal),
  and that letter is reachable from only one input while decoys are reachable
  from many.

Pure Go, no cgo. Built on [`go-text/typesetting`](https://github.com/go-text/typesetting),
a pure-Go HarfBuzz port plus OpenType parser.

## Install

```
go build -o fontleak .
```

## Usage

```
fontleak scan    <file>                 extract, normalize, inspect and solve; full report
fontleak extract <doc> [-o dir]         dump deobfuscated embedded fonts
fontleak inspect <font>                 static analysis (strings, GSUB, homoglyphs)
fontleak shape   <font> "text"          forward shaping oracle (input to glyphs)
fontleak solve   <font> [-mode auto|checker|ligature|feistel] [-alphabet 0123456789abcdef]
fontleak verify  <font> "PVIB{...}"     test whether the font accepts an input
fontleak decode  <font> ["text"]        reveal cmap/homoglyph-hidden text (what the font really draws)
```

`<file>` may be:

* a loose font: `.ttf`/`.otf`, **WOFF**, **WOFF2** (brotli), a TrueType
  collection, or a dfont;
* an **OOXML** document (`.docx`/`.pptx`/`.xlsx`): embedded fonts are unzipped and
  de-obfuscated automatically;
* a **PDF**: embedded font programs (FontFile/FontFile2/FontFile3) are extracted
  and inflated;
* a **CSS/HTML/SVG** file: `data:` URI fonts are base64-decoded.

When a source holds several fonts, the logic-bearing one (most GSUB lookups) is
used. (WOFF2 reconstruction keeps GSUB/cmap exactly; a transformed `glyf` table
is replaced with empty outlines, so outline analysis is skipped for such fonts.)

## How it works, stage by stage

1. **Extract.** Unzips OOXML, reads `fontTable.xml` and its relationships, and
   de-obfuscates each embedded `.odttf` (Microsoft's GUID-keyed XOR). Empty
   references (a common red herring) are detected and skipped.
2. **Normalize.** Repairs sfnt directories where layout tables were renamed (for
   example `GSUB` turned into `ykm|`) and their lengths falsified, so any parser
   can read them. Safe on already-valid fonts.
3. **Inspect.** Embedded strings, flag-shaped substrings, a GSUB lookup summary,
   confusable code points, plus hidden data beyond GSUB (below).
4. **Solve.** Detects the checker / ligature design and recovers the secret.

### Hidden data beyond GSUB

Not every font hides its secret in shaping logic. fontleak also surfaces:

* **cmap / homoglyph text hiding**, where the visible text differs from the
  underlying bytes (a character is mapped to a glyph that draws a different
  letter). `decode <font> "text"` shows what the font actually renders, and
  `decode <font>` lists the remapped characters. The drawn letter is found by
  matching each glyph's outline to a glyph whose *name* is that letter, so it is
  immune to cmap tampering.
* **Hidden tables and trailing data**: non-standard sfnt tables and bytes after
  the last table are dumped, scanned for flag-shaped strings, and auto-decoded
  (hex / base64 / zlib), reporting anything that yields printable text or a font.

### Solving a checker cipher

Two inversion strategies, tried in order:

* **Black-box search** treats the font as an oracle and inverts position-local or
  triangular ciphers directly. Fast, and enough for many CTF fonts.
* **White-box round inversion** (`-mode feistel`) is for strong ciphers. The full
  cipher is full-diffusion (every output depends on every input), so search
  cannot crack it. But the cipher is built in GSUB as a sequence of rounds, and
  each round mixes only neighbouring positions. fontleak runs its own
  lookup-by-lookup GSUB engine (validated against HarfBuzz, including chained
  context formats 1, 2 and 3), splits the pipeline into rounds at the clean
  column states, inverts each round with a cheap local search, and chains the
  inverses back to the typed input. The frame (the literal text around the
  secret), the secret width and the per-column alphabet size are all derived from
  the font, so this is not tied to one challenge's layout.

## Example: NEW_COASTER (PVIB CTF)

This challenge is from the [PVIB CTF](https://www.pvib.nl/actueel/evenementen/ctf-2)
(hence the `PVIB{...}` flag format).

```
$ fontleak scan NEW_COASTER.docx
...
── Theme Park Sans™ [Regular] (...!word/fonts/font11.odttf)
   GSUB: 333 lookups, 17249 ligatures map[ChainedContextualSubst:88 ...]
   • GSUB contains 333 lookups; far more than normal typography, so this font likely encodes logic.
   CHECKER detected: input PVIB{<secret>} renders "Your Flag Is Correct" (secret length 44)
   ★ SOLVED (white-box round inversion): PVIB{4665697374656c20466f6e743a205468652052696465}
     verified by white-box round inversion (recovered input collapses to the success phrase); secret decodes to ASCII "Feistel Font: The Ride"
```

The font implements a 54-round Feistel cipher entirely in GSUB. The hex decodes
to ASCII *"Feistel Font: The Ride"*, a theme-park pun on the cipher.

You can also confirm a guess directly with the font's own self-check:

```
$ fontleak verify NEW_COASTER.docx "PVIB{4665697374656c20466f6e743a205468652052696465}"
renders:  "Your Flag Is Correct"
result:   ✓ ACCEPTED
```

## Example: FONT LEAGUES (TFCCTF 2025)

The sister challenge is a ligature-collapse font: typing the right flag makes it
draw a big "O". fontleak recovers it without that hint.

```
$ fontleak solve -mode ligature Arial-custom.ttf
★ LIGATURE-COLLAPSE SOLVED: typing this makes the font draw 'O' (the success glyph):
  1f89a957a0816e3bea3fa026cd9a47cf181fb2c0e0c9e9442a2c783b01c083d2
  ('O' is drawn by 1 input(s); other letters are decoys)
```

The font has 2091 ligatures and 413 inputs that collapse to a letter; 412 of
them draw a decoy 'X', and exactly one draws the 'O' that the challenge rewards.

## Scope and limits

* Extraction, normalization, inspection, checker and ligature detection, target
  extraction and verification are generic.
* Black-box recovery works for position-local and triangular ciphers.
* White-box round inversion works for round ciphers whose individual rounds have
  local diffusion (Feistel/SPN constructions), which covers NEW_COASTER. A cipher
  that is full-diffusion *within a single round* would still resist it; in that
  case fontleak reports the target glyph sequence, the success phrase and the
  secret length, and lets you `verify` candidates.

## Layout

```
extract/   OOXML and .odttf deobfuscation
sfnt/      table-directory normalization (rename and length repair)
gsub/      GSUB rule extraction (single/multiple/ligature, glyph and char maps)
oracle/    HarfBuzz forward-shaping wrapper
sim/       lookup-by-lookup GSUB engine + white-box round inversion
inspect/   static analysis (strings, regex, lookup stats, homoglyphs)
solve/     checker inversion (black-box + white-box) and ligature expansion
report/    scan pipeline
main.go    CLI
```

## Tests

```
go test ./...
```

Tests run against the challenge artifacts when present: byte-exact deobfuscation
versus the reference, normalization round-trip, the GSUB engine matching HarfBuzz
exactly, checker detection and verification, black-box inversion of a synthetic
tractable cipher, full white-box recovery of the NEW_COASTER flag, and
self-verified ligature recovery of the FONT LEAGUES flag.
