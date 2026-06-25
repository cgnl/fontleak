// Package sim is a small GSUB substitution engine. Unlike the HarfBuzz oracle
// (which only runs the whole pipeline forward), sim applies lookups one at a
// time and can therefore be run in reverse, lookup by lookup. That makes it
// possible to invert a font that implements an invertible cipher in GSUB (e.g.
// a Feistel network), recovering the typed input from a target glyph sequence.
//
// The forward path is validated against HarfBuzz before the inverse is trusted.
package sim

import (
	gtfont "github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/font/opentype/tables"
)

type GID = opentype.GID

// Engine holds the ordered list of GSUB lookups for one feature.
type Engine struct {
	face    *gtfont.Face
	lookups []font_lookup  // all lookups, indexed by lookup-list index
	order   []int          // feature lookup indices, applied in this order
	names   map[string]GID // cached glyph-name -> gid index (lazily built)
}

type font_lookup struct {
	subtables []tables.GSUBLookup
}

// New builds an engine for the given feature tag (e.g. "rlig"). It applies the
// feature's lookups in ascending lookup-list index order (the OpenType rule).
func New(face *gtfont.Face, featureTag string) *Engine {
	e := &Engine{face: face}
	gsub := face.Font.GSUB
	for _, lk := range gsub.Lookups {
		e.lookups = append(e.lookups, font_lookup{subtables: lk.Subtables})
	}
	tag := opentype.MustNewTag(featureTag)
	idxSet := map[int]bool{}
	for _, fr := range gsub.Features {
		if fr.Tag == tag {
			for _, li := range fr.LookupListIndices {
				idxSet[int(li)] = true
			}
		}
	}
	for i := range e.lookups {
		if idxSet[i] {
			e.order = append(e.order, i)
		}
	}
	return e
}

// Glyphs returns the cmap glyph ids for a string (the shaping input).
func (e *Engine) Glyphs(s string) []GID {
	var out []GID
	for _, r := range s {
		if g, ok := e.face.Font.NominalGlyph(r); ok {
			out = append(out, g)
		}
	}
	return out
}

// Forward applies every feature lookup once, in order, returning the result.
func (e *Engine) Forward(buf []GID) []GID {
	for _, idx := range e.order {
		buf = e.applyLookup(idx, buf)
	}
	return buf
}

// Order returns the feature lookup indices in application order.
func (e *Engine) Order() []int { return e.order }

// ForwardRange applies the feature lookups e.order[lo:hi] to a copy of buf.
func (e *Engine) ForwardRange(buf []GID, lo, hi int) []GID {
	out := append([]GID(nil), buf...)
	for i := lo; i < hi && i < len(e.order); i++ {
		out = e.applyLookup(e.order[i], out)
	}
	return out
}

// NumOrder is the number of feature lookups.
func (e *Engine) NumOrder() int { return len(e.order) }

// Name returns a glyph's post-table name (for diagnostics).
func (e *Engine) Name(g GID) string { return e.face.Font.GlyphName(g) }

// Step represents the buffer state after one feature lookup ran.
type Step struct {
	LookupIndex int
	Buf         []GID
	Changed     bool
}

// ForwardTrace runs the pipeline and records the buffer after every feature
// lookup that changed it (for understanding the cipher's round structure).
func (e *Engine) ForwardTrace(buf []GID) []Step {
	var steps []Step
	for _, idx := range e.order {
		nb := e.applyLookup(idx, append([]GID(nil), buf...))
		changed := !sameGIDs(nb, buf)
		buf = nb
		if changed {
			steps = append(steps, Step{LookupIndex: idx, Buf: append([]GID(nil), buf...), Changed: true})
		}
	}
	return steps
}

func sameGIDs(a, b []GID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// applyLookup applies one lookup (all its subtables) over the buffer in a single
// left-to-right pass, advancing past produced glyphs (standard GSUB semantics).
func (e *Engine) applyLookup(idx int, buf []GID) []GID {
	if idx < 0 || idx >= len(e.lookups) {
		return buf
	}
	for _, st := range e.lookups[idx].subtables {
		buf = e.applySubtable(st, buf)
	}
	return buf
}

func (e *Engine) applySubtable(st tables.GSUBLookup, buf []GID) []GID {
	switch s := st.(type) {
	case tables.SingleSubs:
		return mapSingle(buf, s)
	case tables.MultipleSubs:
		return mapMultiple(buf, s)
	case tables.LigatureSubs:
		return e.mapLigature(buf, s)
	case tables.ChainedContextualSubs:
		return e.mapChained(buf, s)
	case tables.ExtensionSubs:
		if inner, ok := unwrap(tables.Extension(s)); ok {
			return e.applySubtable(inner, buf)
		}
	}
	return buf
}

func mapSingle(buf []GID, s tables.SingleSubs) []GID {
	out := make([]GID, len(buf))
	for i, g := range buf {
		out[i] = singleOut(s, g)
	}
	return out
}

// singleOut returns the substitute for g, or g itself if not covered.
func singleOut(s tables.SingleSubs, g GID) GID {
	switch d := s.Data.(type) {
	case tables.SingleSubstData1:
		if _, ok := d.Coverage.Index(tables.GlyphID(g)); ok {
			return GID(uint16(int(g) + int(d.DeltaGlyphID)))
		}
	case tables.SingleSubstData2:
		if i, ok := d.Coverage.Index(tables.GlyphID(g)); ok && i < len(d.SubstituteGlyphIDs) {
			return GID(d.SubstituteGlyphIDs[i])
		}
	}
	return g
}

func mapMultiple(buf []GID, s tables.MultipleSubs) []GID {
	var out []GID
	for _, g := range buf {
		if i, ok := s.Coverage.Index(tables.GlyphID(g)); ok && i < len(s.Sequences) {
			for _, x := range s.Sequences[i].SubstituteGlyphIDs {
				out = append(out, GID(x))
			}
			continue
		}
		out = append(out, g)
	}
	return out
}

func (e *Engine) mapLigature(buf []GID, s tables.LigatureSubs) []GID {
	var out []GID
	i := 0
	for i < len(buf) {
		g := buf[i]
		matched := false
		if ci, ok := s.Coverage.Index(tables.GlyphID(g)); ok && ci < len(s.LigatureSets) {
			for _, lig := range s.LigatureSets[ci].Ligatures {
				comp := lig.ComponentGlyphIDs
				if i+len(comp)+1 <= len(buf) && matchComponents(buf[i+1:], comp) {
					out = append(out, GID(lig.LigatureGlyph))
					i += len(comp) + 1
					matched = true
					break
				}
			}
		}
		if !matched {
			out = append(out, g)
			i++
		}
	}
	return out
}

func matchComponents(buf []GID, comp []tables.GlyphID) bool {
	if len(buf) < len(comp) {
		return false
	}
	for k, c := range comp {
		if buf[k] != GID(c) {
			return false
		}
	}
	return true
}

func (e *Engine) mapChained(buf []GID, s tables.ChainedContextualSubs) []GID {
	switch d := s.Data.(type) {
	case tables.ChainedContextualSubs3:
		return e.chainFmt3(buf, tables.ChainedSequenceContextFormat3(d))
	case tables.ChainedContextualSubs1:
		return e.chainFmt1(buf, tables.ChainedSequenceContextFormat1(d))
	}
	return buf
}

func (e *Engine) chainFmt3(buf []GID, d tables.ChainedSequenceContextFormat3) []GID {
	inLen := len(d.InputCoverages)
	pos := 0
	for pos < len(buf) {
		if e.matchFmt3(buf, pos, d) {
			var grow int
			buf, grow = e.applyRecords(buf, pos, d.SeqLookupRecords)
			pos += inLen + grow
		} else {
			pos++
		}
	}
	return buf
}

func (e *Engine) matchFmt3(buf []GID, pos int, d tables.ChainedSequenceContextFormat3) bool {
	inLen := len(d.InputCoverages)
	if pos+inLen > len(buf) {
		return false
	}
	for k, cov := range d.InputCoverages {
		if _, ok := cov.Index(tables.GlyphID(buf[pos+k])); !ok {
			return false
		}
	}
	for k, cov := range d.BacktrackCoverages {
		j := pos - 1 - k
		if j < 0 {
			return false
		}
		if _, ok := cov.Index(tables.GlyphID(buf[j])); !ok {
			return false
		}
	}
	for k, cov := range d.LookaheadCoverages {
		j := pos + inLen + k
		if j >= len(buf) {
			return false
		}
		if _, ok := cov.Index(tables.GlyphID(buf[j])); !ok {
			return false
		}
	}
	return true
}

func (e *Engine) chainFmt1(buf []GID, d tables.ChainedSequenceContextFormat1) []GID {
	pos := 0
	for pos < len(buf) {
		applied := false
		g := buf[pos]
		// Format 1 is coverage-of-first-glyph + per-glyph rule sets. go-text
		// exposes the rule sets in coverage-index order; we need the first
		// glyph's coverage. It is implicit via the rule set index, so we scan
		// rule sets and match by explicit input sequence.
		for _, rs := range d.ChainedSeqRuleSet {
			for _, r := range rs.ChainedSeqRules {
				inLen := len(r.InputSequence) + 1
				if pos+inLen > len(buf) {
					continue
				}
				if buf[pos] != g { // sanity
					continue
				}
				ok := true
				for k, gid := range r.InputSequence {
					if buf[pos+1+k] != GID(gid) {
						ok = false
						break
					}
				}
				if ok {
					for k, gid := range r.BacktrackSequence {
						j := pos - 1 - k
						if j < 0 || buf[j] != GID(gid) {
							ok = false
							break
						}
					}
				}
				if ok {
					for k, gid := range r.LookaheadSequence {
						j := pos + inLen + k
						if j >= len(buf) || buf[j] != GID(gid) {
							ok = false
							break
						}
					}
				}
				if ok {
					var grow int
					buf, grow = e.applyRecords(buf, pos, r.SeqLookupRecords)
					pos += inLen + grow
					applied = true
					break
				}
			}
			if applied {
				break
			}
		}
		if !applied {
			pos++
		}
	}
	return buf
}

// applyRecords applies nested lookups at (pos+SequenceIndex), substituting the
// single glyph at that position. Nested lookups may change length (e.g. a
// MultipleSubst that inserts a seed glyph), so we splice the result in and
// return how much the buffer grew, plus the (possibly new) buffer. Later
// records' SequenceIndex are adjusted by the running growth.
func (e *Engine) applyRecords(buf []GID, pos int, recs []tables.SequenceLookupRecord) ([]GID, int) {
	totalGrow := 0
	for _, rec := range recs {
		at := pos + int(rec.SequenceIndex) + totalGrow
		if at < 0 || at >= len(buf) {
			continue
		}
		repl := e.applyLookup(int(rec.LookupListIndex), []GID{buf[at]})
		grow := len(repl) - 1
		out := make([]GID, 0, len(buf)+grow)
		out = append(out, buf[:at]...)
		out = append(out, repl...)
		out = append(out, buf[at+1:]...)
		buf = out
		totalGrow += grow
	}
	return buf, totalGrow
}

func unwrap(ext tables.Extension) (tables.GSUBLookup, bool) {
	off := int(ext.ExtensionOffset)
	if off < 0 || off > len(ext.RawData) {
		return nil, false
	}
	src := ext.RawData[off:]
	switch ext.ExtensionLookupType {
	case 1:
		if v, _, err := tables.ParseSingleSubs(src); err == nil {
			return v, true
		}
	case 2:
		if v, _, err := tables.ParseMultipleSubs(src); err == nil {
			return v, true
		}
	case 4:
		if v, _, err := tables.ParseLigatureSubs(src); err == nil {
			return v, true
		}
	case 6:
		if v, _, err := tables.ParseChainedContextualSubs(src); err == nil {
			return v, true
		}
	}
	return nil, false
}
