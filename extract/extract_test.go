package extract

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// docx and the reference docx2ttf.py output live one dir up (the challenge dir).
const docxPath = "../../NEW_COASTER.docx"
const refRegular = "../../NEW_COASTER.docx.fonts/ThemeParkSans™Regular.ttf"

func TestExtractNewCoaster(t *testing.T) {
	if _, err := os.Stat(docxPath); err != nil {
		t.Skipf("challenge docx not present: %v", err)
	}
	fonts, err := FromFile(docxPath)
	if err != nil {
		t.Fatalf("FromFile: %v", err)
	}

	var reg *Font
	var sawBold bool
	for i := range fonts {
		f := &fonts[i]
		if strings.Contains(f.Name, "Theme Park") {
			if f.Style == "Regular" && len(f.Data) > 0 {
				reg = f
			}
			if strings.HasPrefix(f.Style, "Bold") {
				sawBold = true
				if len(f.Data) != 0 {
					t.Errorf("expected Theme Park Bold to be empty (red herring), got %d bytes", len(f.Data))
				}
			}
		}
	}
	if reg == nil {
		t.Fatalf("did not recover a non-empty Theme Park Sans Regular font")
	}
	if !sfntMagic(reg.Data) {
		t.Fatalf("recovered regular font is not valid sfnt: %x", reg.Data[:4])
	}
	if !sawBold {
		t.Errorf("expected to see a Bold entry (even if empty)")
	}

	// Strongest check: byte-identical to the reference docx2ttf.py output.
	if ref, err := os.ReadFile(refRegular); err == nil {
		if !bytes.Equal(reg.Data, ref) {
			t.Errorf("deobfuscated regular font differs from docx2ttf.py reference (len got=%d ref=%d)", len(reg.Data), len(ref))
			// show first divergence
			n := len(reg.Data)
			if len(ref) < n {
				n = len(ref)
			}
			for i := 0; i < n; i++ {
				if reg.Data[i] != ref[i] {
					t.Errorf("first diff at byte %d: got %02x ref %02x", i, reg.Data[i], ref[i])
					break
				}
			}
		}
	} else {
		t.Logf("reference %s not present, skipping byte-identity check", refRegular)
	}
}

func TestKeyArtifactDetection(t *testing.T) {
	key := "{0400E4F7-4B0E-294F-94CF-6BF268531481}"
	got, err := Deobfuscate(make([]byte, 0), key)
	if err == nil {
		t.Fatalf("expected error deobfuscating empty input, got %x", got)
	}
}

// Obfuscation is XOR - applying Deobfuscate twice must be the identity, so we
// can roundtrip any font through it.
func TestDeobfuscateRoundtrip(t *testing.T) {
	key := "{4C132B72-9393-604B-AA99-4EB2ABADE085}"
	orig := make([]byte, 100)
	for i := range orig {
		orig[i] = byte(i*7 + 3)
	}
	obf, err := Deobfuscate(orig, key) // XOR is its own inverse
	if err != nil {
		t.Fatalf("obfuscate: %v", err)
	}
	back, err := Deobfuscate(obf, key)
	if err != nil {
		t.Fatalf("deobfuscate: %v", err)
	}
	for i := range orig {
		if back[i] != orig[i] {
			t.Fatalf("roundtrip mismatch at %d: %02x != %02x", i, back[i], orig[i])
		}
	}
	// Only the first 32 bytes are touched.
	for i := 32; i < len(orig); i++ {
		if obf[i] != orig[i] {
			t.Fatalf("byte %d should be untouched", i)
		}
	}
}
