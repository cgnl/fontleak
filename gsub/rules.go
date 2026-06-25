// Package gsub extracts substitution rules from a font's GSUB table into a
// flat, reusable form: single (1->1), multiple (1->many) and ligature
// (many->1) maps, plus helper mappings (glyph<->char). The contextual and
// chained lookups are summarised but not flattened - they orchestrate the
// others rather than carrying data themselves.
package gsub

import (
	gtfont "github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/font/opentype/tables"
)

type GID = opentype.GID

// Ligature is a many->one rule: Components (the full input sequence, including
// the first/covered glyph) substitute to Out.
type Ligature struct {
	Components []GID
	Out        GID
}

// Rules holds the flattened GSUB content.
type Rules struct {
	Single    map[GID]GID
	Multiple  map[GID][]GID
	Ligatures []Ligature

	// Glyph2Char maps a glyph to a unicode rune via the cmap (base glyphs).
	Glyph2Char map[GID]rune
	// Represents maps a substitution *output* glyph to the rune of the *input*
	// glyph it stands for (e.g. the cmap glyph for 'a' -> g_aA gives g_aA -> 'a').
	Represents map[GID]rune

	// LookupTypeCounts counts subtable kinds (for the inspector/report).
	LookupTypeCounts map[string]int
	NumLookups       int
}

// Extract walks the GSUB lookups of face and returns the flattened rules.
func Extract(face *gtfont.Face) *Rules {
	r := &Rules{
		Single:           map[GID]GID{},
		Multiple:         map[GID][]GID{},
		Glyph2Char:       map[GID]rune{},
		Represents:       map[GID]rune{},
		LookupTypeCounts: map[string]int{},
	}

	// cmap reverse (glyph -> first rune).
	it := face.Cmap.Iter()
	for it.Next() {
		ru, g := it.Char()
		if _, ok := r.Glyph2Char[g]; !ok {
			r.Glyph2Char[g] = ru
		}
	}

	r.NumLookups = len(face.Font.GSUB.Lookups)
	for _, lk := range face.Font.GSUB.Lookups {
		for _, st := range lk.Subtables {
			r.process(st)
		}
	}

	// Build Represents from single rules: output glyph stands for input's char.
	for in, out := range r.Single {
		if c, ok := r.Glyph2Char[in]; ok {
			if _, exists := r.Represents[out]; !exists {
				r.Represents[out] = c
			}
		}
	}
	return r
}

// process handles a single GSUB subtable, recursing through ExtensionSubs.
func (r *Rules) process(st tables.GSUBLookup) {
	switch s := st.(type) {
	case tables.SingleSubs:
		r.LookupTypeCounts["SingleSubst"]++
		switch d := s.Data.(type) {
		case tables.SingleSubstData1:
			for _, g := range coverageGlyphs(d.Coverage) {
				r.Single[g] = GID(uint16(int(g) + int(d.DeltaGlyphID)))
			}
		case tables.SingleSubstData2:
			for i, g := range coverageGlyphs(d.Coverage) {
				if i < len(d.SubstituteGlyphIDs) {
					r.Single[g] = GID(d.SubstituteGlyphIDs[i])
				}
			}
		}
	case tables.MultipleSubs:
		r.LookupTypeCounts["MultipleSubst"]++
		cov := coverageGlyphs(s.Coverage)
		for i, g := range cov {
			if i < len(s.Sequences) {
				seq := s.Sequences[i].SubstituteGlyphIDs
				out := make([]GID, len(seq))
				for j, x := range seq {
					out[j] = GID(x)
				}
				r.Multiple[g] = out
			}
		}
	case tables.AlternateSubs:
		r.LookupTypeCounts["AlternateSubst"]++
	case tables.LigatureSubs:
		r.LookupTypeCounts["LigatureSubst"]++
		cov := coverageGlyphs(s.Coverage)
		for i, g := range cov {
			if i >= len(s.LigatureSets) {
				continue
			}
			for _, lig := range s.LigatureSets[i].Ligatures {
				comps := make([]GID, 0, len(lig.ComponentGlyphIDs)+1)
				comps = append(comps, g)
				for _, c := range lig.ComponentGlyphIDs {
					comps = append(comps, GID(c))
				}
				r.Ligatures = append(r.Ligatures, Ligature{Components: comps, Out: GID(lig.LigatureGlyph)})
			}
		}
	case tables.ContextualSubs:
		r.LookupTypeCounts["ContextualSubst"]++
	case tables.ChainedContextualSubs:
		r.LookupTypeCounts["ChainedContextualSubst"]++
	case tables.ReverseChainSingleSubs:
		r.LookupTypeCounts["ReverseChainSingleSubst"]++
	case tables.ExtensionSubs:
		r.LookupTypeCounts["Extension"]++
		if inner, ok := unwrapExtension(tables.Extension(s)); ok {
			r.process(inner)
		}
	default:
		r.LookupTypeCounts["Other"]++
	}
}

// unwrapExtension re-parses the inner subtable an ExtensionSubs points to.
func unwrapExtension(e tables.Extension) (tables.GSUBLookup, bool) {
	off := int(e.ExtensionOffset)
	if off < 0 || off > len(e.RawData) {
		return nil, false
	}
	src := e.RawData[off:]
	switch e.ExtensionLookupType {
	case 1:
		if v, _, err := tables.ParseSingleSubs(src); err == nil {
			return v, true
		}
	case 2:
		if v, _, err := tables.ParseMultipleSubs(src); err == nil {
			return v, true
		}
	case 3:
		if v, _, err := tables.ParseAlternateSubs(src); err == nil {
			return v, true
		}
	case 4:
		if v, _, err := tables.ParseLigatureSubs(src); err == nil {
			return v, true
		}
	case 5:
		if v, _, err := tables.ParseContextualSubs(src); err == nil {
			return v, true
		}
	case 6:
		if v, _, err := tables.ParseChainedContextualSubs(src); err == nil {
			return v, true
		}
	}
	return nil, false
}

// coverageGlyphs returns the covered glyphs in coverage-index order.
func coverageGlyphs(cov tables.Coverage) []GID {
	switch c := cov.(type) {
	case tables.Coverage1:
		out := make([]GID, len(c.Glyphs))
		for i, g := range c.Glyphs {
			out[i] = GID(g)
		}
		return out
	case tables.Coverage2:
		var out []GID
		for _, rng := range c.Ranges {
			for g := int(rng.StartGlyphID); g <= int(rng.EndGlyphID); g++ {
				out = append(out, GID(uint16(g)))
			}
		}
		return out
	}
	return nil
}
