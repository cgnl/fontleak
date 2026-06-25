package solve

import (
	"bytes"
	"os"
	"testing"

	"github.com/cgnl/fontleak/gsub"
	gtfont "github.com/go-text/typesetting/font"
)

const flKnownContent = "1f89a957a0816e3bea3fa026cd9a47cf181fb2c0e0c9e9442a2c783b01c083d2"

// FONT LEAGUES: the verified ligature solver must auto-select the flag as the
// top result (the rare success letter 'O'), not bury it among 'X' decoys.
func TestSolveLigatureVerifiedFontLeagues(t *testing.T) {
	data, err := os.ReadFile("../../fontleagues_test/Arial-custom.ttf")
	if err != nil {
		t.Skipf("FONT LEAGUES font not present: %v", err)
	}
	face, err := gtfont.ParseTTF(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	rules := gsub.Extract(face)
	v := SolveLigatureVerified(face, rules, 16)
	if len(v) == 0 {
		t.Fatal("no verified candidates")
	}
	top := v[0]
	t.Logf("top: draws %q len=%d: %s", top.Draws, top.Length, top.Expansion)
	if top.Expansion != flKnownContent {
		t.Fatalf("top candidate %q != flag %q", top.Expansion, flKnownContent)
	}
	if top.Draws != 'O' {
		t.Errorf("expected flag to draw 'O', got %q", top.Draws)
	}
	t.Logf("✓ auto-solved: TFCCTF{%s} (draws 'O')", top.Expansion)
}
