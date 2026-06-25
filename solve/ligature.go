package solve

import (
	"sort"
	"strings"

	"github.com/cgnl/fontleak/gsub"
)

// LigatureCandidate is a string recovered by expanding a "final form" glyph
// back to the base characters that produce it.
type LigatureCandidate struct {
	Expansion string // the recovered characters
	AllHex    bool   // whether Expansion is all hex (0-9a-f)
	Length    int
	ViaGlyph  string // glyph name whose expansion produced this (for reporting)
}

// SolveLigature implements the FONT LEAGUES decoding method: a font hides a flag
// by chaining typed characters into ligatures that collapse to a single glyph.
// "Final form" glyphs (produced but never consumed) are expanded recursively
// back to their base characters; long/hex expansions are flag candidates.
//
// minLen filters out trivial expansions (the writeup used >= 32). glyphNames
// maps GID -> name for reporting.
func SolveLigature(r *gsub.Rules, minLen int, glyphNames func(gsub.GID) string) []LigatureCandidate {
	// Reverse single map: output -> input (for tracing a glyph back to its source).
	revSingle := map[gsub.GID]gsub.GID{}
	for in, out := range r.Single {
		if _, exists := revSingle[out]; !exists {
			revSingle[out] = in
		}
	}
	// Map each ligature output glyph to the component sequence that forms it.
	ligByOut := map[gsub.GID][]gsub.GID{}
	for _, lig := range r.Ligatures {
		if _, exists := ligByOut[lig.Out]; !exists {
			ligByOut[lig.Out] = lig.Components
		}
	}

	// expand turns a glyph into the base characters it ultimately represents.
	var expand func(g gsub.GID, depth int, seen map[gsub.GID]bool) string
	expand = func(g gsub.GID, depth int, seen map[gsub.GID]bool) string {
		if depth > 512 || seen[g] {
			return ""
		}
		seen[g] = true
		defer delete(seen, g)

		// A glyph that stands for a typed character (e.g. hexchar -> g_aX reversed).
		if c, ok := r.Represents[g]; ok {
			return string(c)
		}
		// A ligature output expands to its components.
		if comps, ok := ligByOut[g]; ok {
			var b strings.Builder
			for _, c := range comps {
				b.WriteString(expand(c, depth+1, seen))
			}
			return b.String()
		}
		// A single-subst output traces back to its input.
		if in, ok := revSingle[g]; ok {
			return expand(in, depth+1, seen)
		}
		// A base glyph with a cmap character.
		if c, ok := r.Glyph2Char[g]; ok {
			return string(c)
		}
		return ""
	}

	// Inputs and outputs across all rule kinds.
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

	// Final forms: produced but never consumed.
	seenCand := map[string]LigatureCandidate{}
	for g := range outputs {
		if inputs[g] {
			continue
		}
		exp := expand(g, 0, map[gsub.GID]bool{})
		if len(exp) < minLen {
			continue
		}
		cand := LigatureCandidate{
			Expansion: exp,
			AllHex:    isHex(exp),
			Length:    len(exp),
			ViaGlyph:  glyphNames(g),
		}
		// Prefer hex candidates; dedupe by expansion.
		if prev, ok := seenCand[exp]; !ok || (!prev.AllHex && cand.AllHex) {
			seenCand[exp] = cand
		}
	}

	var out []LigatureCandidate
	for _, c := range seenCand {
		out = append(out, c)
	}
	// Hex first, then longest first.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AllHex != out[j].AllHex {
			return out[i].AllHex
		}
		return out[i].Length > out[j].Length
	})
	return out
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
