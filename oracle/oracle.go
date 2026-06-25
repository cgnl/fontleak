// Package oracle wraps the go-text HarfBuzz port to provide a forward shaping
// oracle: given input text it returns the resulting glyph sequence (the same
// transformation a word processor or browser performs). This is the engine the
// checker-style solver searches against.
package oracle

import (
	"bytes"
	"sort"
	"strings"
	"unicode"

	gtfont "github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/harfbuzz"
)

// GID identifies a glyph in the font.
type GID = opentype.GID

// Shaper holds a parsed font ready for repeated shaping.
type Shaper struct {
	Face    *gtfont.Face
	hbFont  *harfbuzz.Font
	g2c     map[GID]rune // reverse cmap: glyph -> first unicode that maps to it
	rligTag opentype.Tag
}

// New builds a Shaper from sfnt bytes (typically post-sfnt.Normalize).
func New(data []byte) (*Shaper, error) {
	face, err := gtfont.ParseTTF(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	s := &Shaper{
		Face:    face,
		hbFont:  harfbuzz.NewFont(face),
		g2c:     map[GID]rune{},
		rligTag: opentype.MustNewTag("rlig"),
	}
	it := face.Cmap.Iter()
	for it.Next() {
		r, g := it.Char()
		if _, ok := s.g2c[g]; !ok {
			s.g2c[g] = r
		}
	}
	return s, nil
}

// Shape shapes text and returns the resulting glyph IDs. The "rlig" feature
// (required ligatures) is explicitly enabled — both known font-CTF designs hide
// their logic there — on top of HarfBuzz's default feature set.
func (s *Shaper) Shape(text string) []GID {
	buf := harfbuzz.NewBuffer()
	runes := []rune(text)
	buf.AddRunes(runes, 0, len(runes))
	buf.GuessSegmentProperties()
	buf.Shape(s.hbFont, []harfbuzz.Feature{
		{Tag: s.rligTag, Value: 1, Start: harfbuzz.FeatureGlobalStart, End: harfbuzz.FeatureGlobalEnd},
	})
	out := make([]GID, len(buf.Info))
	for i, gi := range buf.Info {
		out[i] = gi.Glyph
	}
	return out
}

// Names returns the glyph names for a shaped sequence.
func (s *Shaper) Names(gids []GID) []string {
	out := make([]string, len(gids))
	for i, g := range gids {
		out[i] = s.GlyphName(g)
	}
	return out
}

// GlyphName returns the post-table name for a glyph, or a g<id> fallback.
func (s *Shaper) GlyphName(g GID) string {
	if n := s.Face.Font.GlyphName(g); n != "" {
		return n
	}
	return "g" + itoa(uint32(g))
}

// Readable maps a shaped glyph sequence back to text via the reverse cmap,
// using '.' for glyphs with no unicode mapping. This is how we recognise when
// shaping produced a human-readable result (e.g. "Your Flag Is Correct").
func (s *Shaper) Readable(gids []GID) string {
	var b strings.Builder
	for _, g := range gids {
		if r, ok := s.g2c[g]; ok {
			b.WriteRune(r)
		} else {
			b.WriteByte('.')
		}
	}
	return b.String()
}

// ReadableScore is the fraction of glyphs that map to a printable ASCII rune.
func (s *Shaper) ReadableScore(gids []GID) float64 {
	if len(gids) == 0 {
		return 0
	}
	n := 0
	for _, g := range gids {
		if r, ok := s.g2c[g]; ok && r < 128 && unicode.IsPrint(r) {
			n++
		}
	}
	return float64(n) / float64(len(gids))
}

// ShapeText shapes text and returns the readable mapping of the output.
func (s *Shaper) ShapeText(text string) string { return s.Readable(s.Shape(text)) }

// GlyphForRune returns the nominal (cmap) glyph for a rune.
func (s *Shaper) GlyphForRune(r rune) (GID, bool) {
	return s.Face.Font.NominalGlyph(r)
}

// RuneForGlyph returns the unicode rune that maps to a glyph, if any.
func (s *Shaper) RuneForGlyph(g GID) (rune, bool) { r, ok := s.g2c[g]; return r, ok }

// SortedGlyphNames returns all glyph names referenced by the reverse cmap,
// sorted — handy for diagnostics.
func (s *Shaper) SortedGlyphNames() []string {
	var out []string
	for g := range s.g2c {
		out = append(out, s.GlyphName(g))
	}
	sort.Strings(out)
	return out
}

func itoa(v uint32) string {
	if v == 0 {
		return "0"
	}
	var buf [10]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
