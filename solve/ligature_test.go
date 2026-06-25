package solve

import (
	"strconv"
	"testing"

	"github.com/cgnl/fontleak/gsub"
)

// Build a synthetic FONT LEAGUES-style font: typed hex chars are mapped to
// hex-digit glyphs (g_aX), then a ligature collapses the whole sequence into a
// single "final form" glyph. Expanding that final form must recover the hex.
func TestSolveLigatureRecoversHex(t *testing.T) {
	r := &gsub.Rules{
		Single:           map[gsub.GID]gsub.GID{},
		Multiple:         map[gsub.GID][]gsub.GID{},
		Glyph2Char:       map[gsub.GID]rune{},
		Represents:       map[gsub.GID]rune{},
		LookupTypeCounts: map[string]int{},
	}

	secret := "deadbeef42"
	var comps []gsub.GID
	for i, c := range secret {
		base := gsub.GID(1000 + i)   // cmap glyph for the typed char
		aglyph := gsub.GID(2000 + i) // g_aX produced by single subst
		r.Glyph2Char[base] = c
		r.Single[base] = aglyph
		comps = append(comps, aglyph)
	}
	// Rebuild Represents the way gsub.Extract does (output stands for input char).
	for in, out := range r.Single {
		r.Represents[out] = r.Glyph2Char[in]
	}
	final := gsub.GID(9999)
	r.Ligatures = append(r.Ligatures, gsub.Ligature{Components: comps, Out: final})

	names := func(g gsub.GID) string { return "g" + strconv.Itoa(int(g)) }
	cands := SolveLigature(r, len(secret), names)
	if len(cands) == 0 {
		t.Fatalf("no ligature candidates recovered")
	}
	if cands[0].Expansion != secret {
		t.Fatalf("expected %q, got %q", secret, cands[0].Expansion)
	}
	if !cands[0].AllHex {
		t.Errorf("expected candidate flagged as hex")
	}
	t.Logf("recovered final-form expansion: %q (via %s)", cands[0].Expansion, cands[0].ViaGlyph)
}
