package sim

import (
	"bytes"
	"math/rand"
	"os"
	"testing"

	"github.com/cgnl/fontleak/oracle"
	"github.com/cgnl/fontleak/sfnt"
	gtfont "github.com/go-text/typesetting/font"
)

const refRegular = "../../NEW_COASTER.docx.fonts/ThemeParkSans™Regular.ttf"

func load(t *testing.T) (*Engine, *oracle.Shaper) {
	t.Helper()
	data, err := os.ReadFile(refRegular)
	if err != nil {
		t.Skipf("reference font not present: %v", err)
	}
	norm, _, err := sfnt.Normalize(data)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	face, err := gtfont.ParseTTF(bytes.NewReader(norm))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sh, err := oracle.New(norm)
	if err != nil {
		t.Fatalf("shaper: %v", err)
	}
	return New(face, "rlig"), sh
}

func eq(a, b []GID) bool {
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

// The forward simulator must reproduce HarfBuzz exactly, otherwise the inverse
// is meaningless. Test on the known flag and many random hex inputs.
func TestForwardMatchesHarfBuzz(t *testing.T) {
	e, sh := load(t)

	inputs := []string{
		"PVIB{4665697374656c20466f6e743a205468652052696465}", // known flag (collapses)
		"PVIB{}",
		"PVIB{00000000000000000000000000000000000000000000}",
		"PVIB{deadbeef}",
		"ABC",
	}
	rng := rand.New(rand.NewSource(99))
	const hexd = "0123456789abcdef"
	for i := 0; i < 40; i++ {
		n := 1 + rng.Intn(46)
		b := make([]byte, n)
		for j := range b {
			b[j] = hexd[rng.Intn(16)]
		}
		inputs = append(inputs, "PVIB{"+string(b)+"}")
	}

	mismatch := 0
	for _, in := range inputs {
		want := sh.Shape(in)
		got := e.Forward(e.Glyphs(in))
		if !eq(want, got) {
			mismatch++
			if mismatch <= 3 {
				t.Errorf("mismatch for %q:\n  hb : %v\n  sim: %v", in, sh.Names(want), namesOf(e, got))
			}
		}
	}
	if mismatch > 0 {
		t.Fatalf("%d/%d inputs mismatched HarfBuzz", mismatch, len(inputs))
	}
	t.Logf("forward simulator matches HarfBuzz on %d inputs", len(inputs))
}

func namesOf(e *Engine, gids []GID) []string {
	out := make([]string, len(gids))
	for i, g := range gids {
		out[i] = e.face.Font.GlyphName(g)
	}
	return out
}
