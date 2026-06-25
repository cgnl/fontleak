package solve

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/cgnl/fontleak/gsub"
	gtfont "github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/font/opentype"
)

// LigatureCandidate is a string recovered by expanding a "final form" glyph
// back to the base characters that produce it.
type LigatureCandidate struct {
	Expansion string // the recovered characters
	AllHex    bool   // whether Expansion is all hex (0-9a-f)
	Length    int
	ViaGlyph  string // glyph name whose expansion produced this (for reporting)
}

// VerifiedLigature is a candidate confirmed by the font's own success signal:
// its final-form glyph visually draws a readable character (e.g. the "big O" of
// FONT LEAGUES), so typing the expansion makes the font render that letter.
type VerifiedLigature struct {
	Draws     rune   // the character the success glyph draws
	Expansion string // the typed input that produces it (the flag content)
	AllHex    bool
	Length    int
}

// buildExpander returns a function that expands a glyph to the base characters
// that produce it, by reversing ligature/multiple/single substitutions.
func buildExpander(r *gsub.Rules) func(gsub.GID) string {
	revSingle := map[gsub.GID]gsub.GID{}
	for in, out := range r.Single {
		if _, ok := revSingle[out]; !ok {
			revSingle[out] = in
		}
	}
	ligByOut := map[gsub.GID][]gsub.GID{}
	for _, lig := range r.Ligatures {
		if _, ok := ligByOut[lig.Out]; !ok {
			ligByOut[lig.Out] = lig.Components
		}
	}
	var expand func(g gsub.GID, depth int, seen map[gsub.GID]bool) string
	expand = func(g gsub.GID, depth int, seen map[gsub.GID]bool) string {
		if depth > 4096 || seen[g] {
			return ""
		}
		seen[g] = true
		defer delete(seen, g)
		if c, ok := r.Represents[g]; ok {
			return string(c)
		}
		if comps, ok := ligByOut[g]; ok {
			var b strings.Builder
			for _, c := range comps {
				b.WriteString(expand(c, depth+1, seen))
			}
			return b.String()
		}
		if in, ok := revSingle[g]; ok {
			return expand(in, depth+1, seen)
		}
		if c, ok := r.Glyph2Char[g]; ok {
			return string(c)
		}
		return ""
	}
	return func(g gsub.GID) string { return expand(g, 0, map[gsub.GID]bool{}) }
}

// finalForms returns glyphs that are produced by a substitution but never
// consumed by one (the terminal glyphs a typed sequence collapses into).
func finalForms(r *gsub.Rules) []gsub.GID {
	inputs := map[gsub.GID]bool{}
	outputs := map[gsub.GID]bool{}
	for in, out := range r.Single {
		inputs[in] = true
		outputs[out] = true
	}
	for in, seq := range r.Multiple {
		inputs[in] = true
		for _, o := range seq {
			outputs[o] = true
		}
	}
	for _, lig := range r.Ligatures {
		for _, c := range lig.Components {
			inputs[c] = true
		}
		outputs[lig.Out] = true
	}
	var out []gsub.GID
	for g := range outputs {
		if !inputs[g] {
			out = append(out, g)
		}
	}
	return out
}

// SolveLigature implements the FONT LEAGUES decoding method: "final form" glyphs
// (produced but never consumed) are expanded recursively back to their base
// characters; long/hex expansions are flag candidates. minLen filters trivial
// expansions; glyphNames maps GID -> name for reporting.
func SolveLigature(r *gsub.Rules, minLen int, glyphNames func(gsub.GID) string) []LigatureCandidate {
	expand := buildExpander(r)
	seenCand := map[string]LigatureCandidate{}
	for _, g := range finalForms(r) {
		exp := expand(g)
		if len(exp) < minLen {
			continue
		}
		cand := LigatureCandidate{Expansion: exp, AllHex: isHex(exp), Length: len(exp), ViaGlyph: glyphNames(g)}
		if prev, ok := seenCand[exp]; !ok || (!prev.AllHex && cand.AllHex) {
			seenCand[exp] = cand
		}
	}
	var out []LigatureCandidate
	for _, c := range seenCand {
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AllHex != out[j].AllHex {
			return out[i].AllHex
		}
		return out[i].Length > out[j].Length
	})
	return out
}

// SolveLigatureVerified auto-selects the real flag from the candidate haystack:
// it finds final-form glyphs whose *outline* matches a readable letter (the
// font's built-in success signal, e.g. FONT LEAGUES' "you get an O if correct"),
// and returns their expansions. Typing such an expansion makes the font draw
// that letter, so the result is self-verifying.
func SolveLigatureVerified(face *gtfont.Face, r *gsub.Rules, minLen int) []VerifiedLigature {
	// Map each readable ASCII single-letter/digit glyph's outline to its
	// character. ASCII-only avoids the font's many non-Latin homoglyph final
	// forms (Cyrillic/Greek look-alikes) that act as decoys.
	letterByOutline := map[string]rune{}
	for g, ch := range r.Glyph2Char {
		if ch >= 128 || (!unicode.IsLetter(ch) && !unicode.IsDigit(ch)) {
			continue
		}
		if s, ok := outlineSignature(face, g); ok {
			// Prefer letters over digits if two map to the same outline.
			if prev, exists := letterByOutline[s]; !exists || (unicode.IsDigit(prev) && unicode.IsLetter(ch)) {
				letterByOutline[s] = ch
			}
		}
	}

	expand := buildExpander(r)
	seen := map[string]VerifiedLigature{}
	for _, g := range finalForms(r) {
		s, ok := outlineSignature(face, g)
		if !ok {
			continue
		}
		ch, ok := letterByOutline[s]
		if !ok {
			continue // this final form does not draw a recognisable letter
		}
		// Skip the trivial case where the final form IS the plain letter glyph.
		if c, isCmap := r.Glyph2Char[g]; isCmap && c == ch {
			continue
		}
		exp := expand(g)
		if len(exp) < minLen {
			continue
		}
		v := VerifiedLigature{Draws: ch, Expansion: exp, AllHex: isHex(exp), Length: len(exp)}
		if prev, ok := seen[exp]; !ok || (!prev.AllHex && v.AllHex) {
			seen[exp] = v
		}
	}
	var out []VerifiedLigature
	for _, v := range seen {
		out = append(out, v)
	}
	// The success letter is the rare one: there is a single correct flag, so the
	// letter it draws has few inputs, while a "wrong" letter (a decoy collapse
	// target) is reachable from many sequences. Rank rarest-drawn-letter first.
	perLetter := map[rune]int{}
	for _, v := range out {
		perLetter[v.Draws]++
	}
	sort.SliceStable(out, func(i, j int) bool {
		if ci, cj := perLetter[out[i].Draws], perLetter[out[j].Draws]; ci != cj {
			return ci < cj
		}
		if out[i].AllHex != out[j].AllHex {
			return out[i].AllHex
		}
		return out[i].Length > out[j].Length
	})
	return out
}

// LetterCount returns how many verified candidates draw each character, so the
// caller can judge which is the unique success signal.
func LetterCount(vs []VerifiedLigature) map[rune]int {
	m := map[rune]int{}
	for _, v := range vs {
		m[v.Draws]++
	}
	return m
}

// outlineSignature builds a stable signature of a glyph's vector outline.
func outlineSignature(face *gtfont.Face, g gsub.GID) (string, bool) {
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

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
