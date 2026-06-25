package sfnt

import (
	"bytes"
	"os"
	"testing"

	gtfont "github.com/go-text/typesetting/font"
)

// Correctly deobfuscated reference (docx2ttf.py output): already has proper
// table tags, so Normalize must be SAFE (not break it).
const refRegular = "../../NEW_COASTER.docx.fonts/ThemeParkSans™Regular.ttf"

// A genuinely table-renamed font (GSUB -> "ykm|", GDEF -> "zkqy", lengths
// falsified). Used to prove the repair path.
const renamedFont = "../../theme_park.ttf"

func gsubLookups(t *testing.T, data []byte) int {
	face, err := gtfont.ParseTTF(bytes.NewReader(data))
	if err != nil {
		return -1
	}
	return len(face.Font.GSUB.Lookups)
}

func TestNormalizeSafeOnCleanFont(t *testing.T) {
	data, err := os.ReadFile(refRegular)
	if err != nil {
		t.Skipf("reference font not present: %v", err)
	}
	before := gsubLookups(t, data)
	if before <= 0 {
		t.Fatalf("expected clean font to parse with GSUB lookups, got %d", before)
	}
	out, _, err := Normalize(data)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	after := gsubLookups(t, out)
	if after != before {
		t.Fatalf("Normalize changed GSUB lookup count on a clean font: %d -> %d", before, after)
	}
	t.Logf("clean font: %d GSUB lookups preserved through Normalize", after)
}

func TestNormalizeRepairsRenamedTables(t *testing.T) {
	data, err := os.ReadFile(renamedFont)
	if err != nil {
		t.Skipf("renamed-table fixture not present: %v", err)
	}
	out, rep, err := Normalize(data)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	var renamedGSUB bool
	for _, c := range rep.Changes {
		if c.NewTag == "GSUB" && c.OldTag != "GSUB" {
			renamedGSUB = true
			t.Logf("repaired %q -> GSUB (len %d -> %d)", c.OldTag, c.OldLength, c.NewLength)
		}
	}
	if !renamedGSUB {
		t.Fatalf("expected to repair a renamed GSUB table")
	}
	if n := gsubLookups(t, out); n <= 0 {
		t.Fatalf("repaired font still exposes no GSUB lookups (%d)", n)
	} else {
		t.Logf("repaired font now exposes %d GSUB lookups", n)
	}
}
