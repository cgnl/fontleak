package oracle

import (
	"os"
	"strings"
	"testing"

	"github.com/cgnl/fontleak/sfnt"
)

const refRegular = "../../NEW_COASTER.docx.fonts/ThemeParkSans™Regular.ttf"

const knownFlag = "PVIB{4665697374656c20466f6e743a205468652052696465}"

func loadShaper(t *testing.T) *Shaper {
	t.Helper()
	data, err := os.ReadFile(refRegular)
	if err != nil {
		t.Skipf("reference font not present: %v", err)
	}
	norm, _, err := sfnt.Normalize(data)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	s, err := New(norm)
	if err != nil {
		t.Fatalf("New shaper: %v", err)
	}
	return s
}

func TestSelfCheckRendersCorrect(t *testing.T) {
	s := loadShaper(t)
	got := s.ShapeText(knownFlag)
	t.Logf("shaping known flag renders: %q", got)
	if !strings.Contains(strings.ToLower(got), "your flag is correct") {
		t.Fatalf("expected self-check 'Your Flag Is Correct', got %q", got)
	}
}

func TestWrongFlagNotReadable(t *testing.T) {
	s := loadShaper(t)
	got := s.ShapeText("PVIB{00000000000000000000000000000000000000000000}")
	if strings.Contains(strings.ToLower(got), "correct") {
		t.Fatalf("wrong flag unexpectedly rendered 'correct': %q", got)
	}
	t.Logf("wrong flag renders (garbage, as expected): %q", got)
}
