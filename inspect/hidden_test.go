package inspect

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"os"
	"strings"
	"testing"

	"github.com/cgnl/fontleak/gsub"
	gtfont "github.com/go-text/typesetting/font"
)

// buildSfntWith creates a minimal (directory-only) sfnt carrying one custom
// table, enough for HiddenData to parse and decode it.
func buildSfntWith(tag string, payload []byte) []byte {
	tags := []struct {
		t string
		d []byte
	}{
		{"head", make([]byte, 54)},
		{tag, payload},
	}
	n := len(tags)
	out := make([]byte, 12+n*16)
	binary.BigEndian.PutUint32(out[0:4], 0x00010000)
	binary.BigEndian.PutUint16(out[4:6], uint16(n))
	off := uint32(12 + n*16)
	for i, tb := range tags {
		rec := 12 + i*16
		copy(out[rec:rec+4], tb.t)
		binary.BigEndian.PutUint32(out[rec+8:rec+12], off)
		binary.BigEndian.PutUint32(out[rec+12:rec+16], uint32(len(tb.d)))
		out = append(out, tb.d...)
		off += uint32(len(tb.d))
	}
	return out
}

func TestHiddenBase64Table(t *testing.T) {
	flag := "FLAG{custom_table_secret}"
	payload := []byte(base64.StdEncoding.EncodeToString([]byte(flag)))
	data := buildSfntWith("hide", payload)

	blobs := HiddenData(data)
	var found bool
	for _, b := range blobs {
		for _, n := range b.Notes {
			if strings.Contains(n, flag) {
				found = true
				t.Logf("%s -> %s", b.Origin, n)
			}
		}
	}
	if !found {
		t.Fatalf("base64-encoded flag in custom table was not decoded; blobs=%+v", blobs)
	}
}

func TestHiddenTrailingData(t *testing.T) {
	data := buildSfntWith("head2", make([]byte, 8))
	data = append(data, []byte("\x00PVIB{trailing_secret}\x00")...)
	blobs := HiddenData(data)
	var found bool
	for _, b := range blobs {
		for _, n := range b.Notes {
			if strings.Contains(n, "PVIB{trailing_secret}") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("flag in trailing data not found; blobs=%+v", blobs)
	}
}

func TestDecodeHomoglyph(t *testing.T) {
	data, err := os.ReadFile("../../fmt_fixtures/homoglyph.ttf")
	if err != nil {
		t.Skipf("homoglyph fixture missing: %v", err)
	}
	face, err := gtfont.ParseTTF(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	rules := gsub.Extract(face)
	got := DecodeRendered(face, rules, "HAIL")
	if got != "WXOR" {
		t.Fatalf("DecodeRendered(\"HAIL\") = %q, want \"WXOR\"", got)
	}
	if len(CmapRemaps(face, rules)) == 0 {
		t.Fatalf("expected cmap remaps to be reported")
	}
	t.Logf("decode HAIL -> %q; remaps detected", got)
}
