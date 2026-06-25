// Package inspect performs static analysis of a parsed font: it surfaces
// embedded strings, flag-shaped substrings, a summary of the GSUB "logic",
// and glyphs that masquerade as other characters (identical outlines mapped to
// different code points - the trick used to hide a flag in plain sight).
package inspect

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/cgnl/fontleak/gsub"
	gtfont "github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/font/opentype"
)

// Report is the static-analysis result.
type Report struct {
	Strings      []string       // notable printable strings embedded in the font
	FlagMatches  []string       // substrings shaped like CTF flags
	LookupCounts map[string]int // GSUB subtable-kind counts
	NumLigatures int
	NumLookups   int
	CmapEntries  int
	Homoglyphs   []string // "char X is drawn identically to char Y" notes
	Verdict      []string // human-readable conclusions
}

// flagRe matches FLAG{...}, PVIB{...}, TFCCTF{...}-style tokens. The brace body
// must be >=4 chars and not contain braces/pipes, to avoid matching a font's
// sequential cmap character dump (e.g. "...xyz{|}").
var flagRe = regexp.MustCompile(`[A-Za-z][A-Za-z0-9_]{1,15}\{[^|{}]{4,256}\}`)

// hexRe matches long hex runs (possible hashes/keys).
var hexRe = regexp.MustCompile(`[0-9a-fA-F]{24,}`)

// distinctHex counts distinct hex digits, to reject low-entropy runs like
// "2f2f2f2f..." that are really font-table bytes, not a hidden hash.
func distinctHex(s string) int {
	seen := map[rune]bool{}
	for _, c := range strings.ToLower(s) {
		seen[c] = true
	}
	return len(seen)
}

// Run analyses the font.
func Run(data []byte, face *gtfont.Face, rules *gsub.Rules) Report {
	rep := Report{
		LookupCounts: rules.LookupTypeCounts,
		NumLigatures: len(rules.Ligatures),
		NumLookups:   rules.NumLookups,
		CmapEntries:  len(rules.Glyph2Char),
	}

	// Embedded strings + flag-shaped tokens from the raw font bytes.
	strs := printableStrings(data, 4)
	seenFlag := map[string]bool{}
	for _, s := range strs {
		for _, m := range flagRe.FindAllString(s, -1) {
			if !seenFlag[m] {
				seenFlag[m] = true
				rep.FlagMatches = append(rep.FlagMatches, m)
			}
		}
	}
	for _, m := range hexRe.FindAllString(strings.Join(strs, "\n"), -1) {
		if len(m) >= 32 && distinctHex(m) >= 6 && !seenFlag[m] {
			seenFlag[m] = true
			rep.FlagMatches = append(rep.FlagMatches, m+" (long hex)")
		}
	}
	rep.Strings = notable(strs)

	rep.Homoglyphs = findHomoglyphs(face, rules)

	rep.Verdict = verdict(rep)
	return rep
}

// printableStrings extracts ASCII printable runs of >= min length.
func printableStrings(data []byte, min int) []string {
	var out []string
	var cur []byte
	flush := func() {
		if len(cur) >= min {
			out = append(out, string(cur))
		}
		cur = cur[:0]
	}
	for _, b := range data {
		if b >= 0x20 && b < 0x7f {
			cur = append(cur, b)
		} else {
			flush()
		}
	}
	flush()
	return out
}

// notable keeps strings that look meaningful (letters + spaces, reasonable
// length) and de-duplicates, so the report isn't drowned in table noise.
func notable(strs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range strs {
		s = strings.TrimSpace(s)
		if len(s) < 5 || seen[s] {
			continue
		}
		letters := 0
		for _, c := range s {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				letters++
			}
		}
		if letters*2 < len(s) {
			continue // mostly non-letters: probably binary noise
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

// findHomoglyphs reports code points whose glyph outline is byte-identical to
// the glyph of a *different* code point - i.e. characters drawn as something
// else. This is exactly how a flag can be hidden: a "garbage" glyph shares its
// outline with a normal letter, or vice-versa.
func findHomoglyphs(face *gtfont.Face, rules *gsub.Rules) []string {
	sigToChars := map[string][]rune{}
	for g, r := range rules.Glyph2Char {
		sig, ok := outlineSig(face, g)
		if !ok {
			continue
		}
		sigToChars[sig] = append(sigToChars[sig], r)
	}
	var out []string
	for _, chars := range sigToChars {
		if len(chars) < 2 {
			continue
		}
		// Only report groups that mix visually-distinct characters.
		uniq := dedupeRunes(chars)
		if len(uniq) < 2 {
			continue
		}
		out = append(out, fmt.Sprintf("identical outline shared by: %s", runesDisplay(uniq)))
		if len(out) >= 20 {
			break
		}
	}
	sort.Strings(out)
	return out
}

// outlineSig builds a stable signature of a glyph's vector outline.
func outlineSig(face *gtfont.Face, g gsub.GID) (string, bool) {
	gd := face.GlyphData(opentype.GID(g))
	o, ok := gd.(gtfont.GlyphOutline)
	if !ok || len(o.Segments) == 0 {
		return "", false
	}
	var b strings.Builder
	for _, s := range o.Segments {
		fmt.Fprintf(&b, "%d", s.Op)
		for _, p := range s.ArgsSlice() {
			fmt.Fprintf(&b, ":%.0f,%.0f", p.X, p.Y)
		}
		b.WriteByte(';')
	}
	return b.String(), true
}

func dedupeRunes(rs []rune) []rune {
	seen := map[rune]bool{}
	var out []rune
	for _, r := range rs {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func runesDisplay(rs []rune) string {
	var parts []string
	for _, r := range rs {
		parts = append(parts, fmt.Sprintf("%q(U+%04X)", r, r))
	}
	return strings.Join(parts, ", ")
}

func verdict(rep Report) []string {
	var v []string
	if rep.NumLookups > 50 {
		v = append(v, fmt.Sprintf("GSUB contains %d lookups (%d ligatures) - far more than normal typography; this font likely encodes logic.", rep.NumLookups, rep.NumLigatures))
	}
	if len(rep.FlagMatches) > 0 {
		v = append(v, "Flag-shaped strings were found embedded in the font (see FlagMatches).")
	}
	if len(v) == 0 {
		v = append(v, "Nothing obviously hidden detected by static analysis.")
	}
	return v
}
