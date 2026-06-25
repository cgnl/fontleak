package solve

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/cgnl/fontleak/gsub"
	"github.com/cgnl/fontleak/sfnt"
	gtfont "github.com/go-text/typesetting/font"
)

const knownFeistelInput = "PVIB{4665697374656c20466f6e743a205468652052696465}"

// White-box round inversion must fully recover the NEW_COASTER flag (a strong
// full-diffusion Feistel that black-box search cannot crack), without being told
// the flag.
func TestSolveFeistelNewCoaster(t *testing.T) {
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
	rules := gsub.Extract(face)

	res := SolveFeistel(face, rules)
	if !res.Found {
		t.Fatalf("white-box inversion failed: %s", res.Note)
	}
	t.Logf("recovered: %s", res.Input)
	t.Logf("note: %s", res.Note)
	if res.Input != knownFeistelInput {
		t.Fatalf("recovered %q != expected %q", res.Input, knownFeistelInput)
	}
	if !strings.Contains(res.Note, "Feistel Font: The Ride") {
		t.Errorf("expected ASCII decode note, got %q", res.Note)
	}
}
