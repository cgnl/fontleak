package solve

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/cgnl/fontleak/gsub"
	"github.com/cgnl/fontleak/oracle"
	"github.com/cgnl/fontleak/sfnt"
	gtfont "github.com/go-text/typesetting/font"
)

const refRegular = "../../NEW_COASTER.docx.fonts/ThemeParkSans™Regular.ttf"
const knownFlag = "PVIB{4665697374656c20466f6e743a205468652052696465}"

func loadNewCoaster(t *testing.T) (*oracle.Shaper, *gsub.Rules) {
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
	return sh, gsub.Extract(face)
}

// The font is a strong full-diffusion cipher; we don't expect blind recovery,
// but we DO expect the tool to detect the checker, extract the target, identify
// the success phrase, and verify the known flag via the font's own self-check.
func TestNewCoasterDetectAndVerify(t *testing.T) {
	sh, rules := loadNewCoaster(t)

	rec, ok := FindRecognition(rules)
	if !ok {
		t.Fatalf("failed to detect recognition rule")
	}
	t.Logf("detected: prefix=%q suffix=%q targetLen=%d phrase=%q",
		rec.Prefix, rec.Suffix, len(rec.Target), rec.Phrase)
	if rec.Prefix != "PVIB{" || rec.Suffix != "}" {
		t.Errorf("unexpected frame: %q ... %q", rec.Prefix, rec.Suffix)
	}
	if len(rec.Target) != 44 {
		t.Errorf("expected 44 target glyphs, got %d", len(rec.Target))
	}

	rendered, ok := Verify(sh, knownFlag)
	if !ok {
		t.Fatalf("Verify(knownFlag) did not render a success phrase, got %q", rendered)
	}
	if !strings.Contains(strings.ToLower(rendered), "your flag is correct") {
		t.Errorf("expected 'Your Flag Is Correct', got %q", rendered)
	}
	t.Logf("Verify(known flag) -> %q ✓", rendered)

	// A wrong flag must not verify.
	if _, ok := Verify(sh, "PVIB{"+strings.Repeat("0", 44)+"}"); ok {
		t.Errorf("wrong flag unexpectedly verified")
	}
}

// The generic blind inverter must solve a *tractable* (position-local /
// triangular) cipher. We build a synthetic oracle and confirm recovery.
func TestInverterSolvesTriangularCipher(t *testing.T) {
	const n = 8
	secret := []rune("c0ffee42")
	alphabet := []rune("0123456789abcdef")
	idx := map[rune]int{}
	for i, c := range alphabet {
		idx[c] = i
	}

	// Triangular cipher: out[i] depends on in[i] and in[i+1] (a small Feistel-ish
	// carry), framed by "PVIB{" ... "}". Output glyphs are gid = value+1000.
	framePre := []gsub.GID{1, 2, 3, 4, 5} // PVIB{
	frameSuf := []gsub.GID{6}             // }
	shape := func(s string) []gsub.GID {
		body := s[len("PVIB{") : len(s)-len("}")]
		x := []rune(body)
		out := append([]gsub.GID(nil), framePre...)
		for i := 0; i < len(x); i++ {
			v := idx[x[i]]
			if i+1 < len(x) {
				v = (v + idx[x[i+1]]*3) % 16
			}
			out = append(out, gsub.GID(1000+v))
		}
		out = append(out, frameSuf...)
		return out
	}

	// target = cipher of the secret.
	target := shape("PVIB{" + string(secret) + "}")[len(framePre) : len(framePre)+n]

	inv := &inverter{
		shape:    shape,
		prefix:   "PVIB{",
		suffix:   "}",
		preN:     len(framePre),
		sufN:     len(frameSuf),
		target:   target,
		alphabet: alphabet,
	}
	got, ok := inv.solve()
	if !ok {
		t.Fatalf("inverter failed on tractable cipher (got %q after %d calls)", got, inv.calls)
	}
	if got != string(secret) {
		t.Fatalf("recovered %q != secret %q", got, string(secret))
	}
	t.Logf("blind inverter recovered %q in %d oracle calls", got, inv.calls)
}
