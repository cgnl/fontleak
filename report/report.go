// Package report ties the pipeline together: extract -> normalize -> parse ->
// inspect -> solve, producing a single human-readable report per font.
package report

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/cgnl/fontleak/extract"
	"github.com/cgnl/fontleak/gsub"
	"github.com/cgnl/fontleak/inspect"
	"github.com/cgnl/fontleak/oracle"
	"github.com/cgnl/fontleak/sfnt"
	"github.com/cgnl/fontleak/solve"
	gtfont "github.com/go-text/typesetting/font"
)

// Options tunes the scan.
type Options struct {
	Alphabet    string // secret alphabet for checker inversion (default hex)
	LigatureMin int    // min length for ligature-expand candidates
	TryInvert   bool   // attempt black-box checker inversion
	MaxStrings  int    // cap on strings printed
}

// DefaultOptions returns sensible defaults.
func DefaultOptions() Options {
	return Options{Alphabet: "0123456789abcdef", LigatureMin: 16, TryInvert: true, MaxStrings: 25}
}

// Scan runs the whole pipeline on a file path and writes a report to w-like
// builder, returning the text.
func Scan(path string, opt Options) (string, error) {
	fonts, err := extract.FromFile(path)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# fontleak scan: %s\n", path)
	fmt.Fprintf(&b, "Recovered %d embedded font reference(s).\n\n", len(fonts))

	// Collect empty/missing companion fonts so a stuck solver can hint at them.
	var emptyCompanions []string
	for _, f := range fonts {
		if len(f.Data) == 0 {
			emptyCompanions = append(emptyCompanions, fmt.Sprintf("%s [%s]", f.Name, f.Style))
		}
	}

	for _, f := range fonts {
		fmt.Fprintf(&b, "── %s [%s] ", f.Name, f.Style)
		if f.Source != "" {
			fmt.Fprintf(&b, "(%s)", f.Source)
		}
		b.WriteByte('\n')
		if len(f.Data) == 0 {
			fmt.Fprintf(&b, "   (no usable font data - skipped)\n\n")
			continue
		}
		reportFont(&b, f, opt, emptyCompanions)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

func reportFont(b *strings.Builder, f extract.Font, opt Options, emptyCompanions []string) {
	norm, nrep, err := sfnt.Normalize(f.Data)
	if err != nil {
		fmt.Fprintf(b, "   normalize: %v\n", err)
		norm = f.Data
	}
	for _, c := range nrep.Changes {
		if c.OldTag != c.NewTag {
			fmt.Fprintf(b, "   normalized table %q -> %s\n", c.OldTag, c.NewTag)
		}
	}

	face, err := gtfont.ParseTTF(bytes.NewReader(norm))
	if err != nil {
		fmt.Fprintf(b, "   parse: %v\n", err)
		return
	}
	rules := gsub.Extract(face)

	rep := inspect.Run(norm, face, rules)
	fmt.Fprintf(b, "   GSUB: %d lookups, %d ligatures %v\n", rep.NumLookups, rep.NumLigatures, rep.LookupCounts)
	if len(rep.FlagMatches) > 0 {
		fmt.Fprintf(b, "   flag-shaped strings: %s\n", strings.Join(rep.FlagMatches, ", "))
	}
	if len(rep.Homoglyphs) > 0 {
		fmt.Fprintf(b, "   %d code-point group(s) share glyph outlines (confusables; run `inspect` for the list)\n", len(rep.Homoglyphs))
	}
	for _, v := range rep.Verdict {
		fmt.Fprintf(b, "   • %s\n", v)
	}

	sh, err := oracle.New(norm)
	if err != nil {
		fmt.Fprintf(b, "   oracle: %v\n", err)
		return
	}

	// Checker-style font (validates a typed flag, renders a success phrase).
	rec, isChecker := solve.FindRecognition(rules)
	if isChecker {
		fmt.Fprintf(b, "   CHECKER detected: input %s<secret>%s renders %q (secret length %d)\n",
			rec.Prefix, rec.Suffix, rec.Phrase, len(rec.Target))
		if opt.TryInvert {
			res := solve.SolveChecker(sh, rules, opt.Alphabet)
			if res.Found {
				fmt.Fprintf(b, "   ★ SOLVED: %s\n", res.Input)
				fmt.Fprintf(b, "     %s\n", res.Note)
			} else {
				fmt.Fprintf(b, "   black-box inversion: %s (oracle calls: %d)\n", res.Note, res.OracleCalls)
				// Strong round ciphers: try white-box lookup-by-lookup inversion.
				fr := solve.SolveFeistel(face, rules)
				if fr.Found {
					fmt.Fprintf(b, "   ★ SOLVED (white-box round inversion): %s\n", fr.Input)
					fmt.Fprintf(b, "     %s\n", fr.Note)
				} else {
					fmt.Fprintf(b, "   white-box inversion: %s\n", fr.Note)
					for _, hint := range solve.Hints(rec, opt.Alphabet, emptyCompanions) {
						fmt.Fprintf(b, "   hint: %s\n", hint)
					}
				}
			}
		}
	}

	// Ligature-collapse font (FONT LEAGUES): expand final forms to candidates.
	cands := solve.SolveLigature(rules, opt.LigatureMin, sh.GlyphName)
	if len(cands) > 0 {
		shown := 0
		for _, c := range cands {
			tag := ""
			if c.AllHex {
				tag = " [hex]"
			}
			fmt.Fprintf(b, "   ligature-candidate%s len=%d: %s\n", tag, c.Length, c.Expansion)
			shown++
			if shown >= 5 {
				break
			}
		}
	}
}
