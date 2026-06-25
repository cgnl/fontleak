// Package extract pulls fonts out of containers (OOXML documents) and
// deobfuscates them, plus accepts loose font files. It is the first stage of
// the fontleak pipeline.
package extract

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// Font is a single recovered font: its logical name/style, the (deobfuscated)
// sfnt bytes, and where it came from.
type Font struct {
	Name   string // e.g. "Theme Park Sans™"
	Style  string // "Regular" | "Bold" | "Italic" | ...
	Source string // path or container-internal name it came from
	Data   []byte // sfnt bytes, ready for sfnt.Normalize / parsing
}

// sfntMagic reports whether b starts with a recognised sfnt signature
// (TrueType 0x00010000, OpenType "OTTO", or "true"/"ttcf").
func sfntMagic(b []byte) bool {
	if len(b) < 4 {
		return false
	}
	switch string(b[:4]) {
	case "OTTO", "true", "ttcf":
		return true
	}
	return b[0] == 0x00 && b[1] == 0x01 && b[2] == 0x00 && b[3] == 0x00
}

// guidKeyBytes parses an OOXML fontKey GUID like
// "{4C132B72-9393-604B-AA99-4EB2ABADE085}" into its 16 raw key bytes, in the
// order Word uses for the embedded-font XOR (the hex digits as written, braces
// and dashes stripped).
func guidKeyBytes(fontKey string) ([]byte, error) {
	s := strings.TrimSpace(fontKey)
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	s = strings.ReplaceAll(s, "-", "")
	if len(s) != 32 {
		return nil, fmt.Errorf("fontKey %q is not 32 hex digits", fontKey)
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("fontKey %q: %w", fontKey, err)
	}
	return b, nil
}

// Deobfuscate reverses Microsoft's "obfuscated font" scheme used for embedded
// fonts in OOXML (.odttf): the first 32 bytes of the file are XORed with the
// 16-byte GUID key repeated twice. This matches the reference docx2ttf.py:
//
//	doubled = key||key (as a 32-byte big-endian integer)
//	out[:32] = (data[:32] as little-endian int) XOR doubled, back to 32 LE bytes
//	out[32:] = data[32:]
//
// We implement that byte order directly: XOR position i (0..31) of the
// little-endian view, i.e. data[i] ^ doubledLE[i], where doubledLE is the
// doubled key written as the little-endian representation of the big-endian
// integer — which is simply (key||key) reversed.
func Deobfuscate(data []byte, fontKey string) ([]byte, error) {
	key, err := guidKeyBytes(fontKey)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty odttf (0 bytes) — nothing to deobfuscate")
	}

	out := make([]byte, len(data))
	copy(out, data)

	// doubled big-endian = key||key (32 bytes). The reference code treats both
	// the data and the key as little-endian integers and XORs them, which is
	// equivalent to XORing the byte-reversed doubled key against the raw bytes.
	doubled := make([]byte, 32)
	copy(doubled[:16], key)
	copy(doubled[16:], key)
	// little-endian view of the big-endian integer = reverse the 32 bytes.
	keyLE := make([]byte, 32)
	for i := 0; i < 32; i++ {
		keyLE[i] = doubled[31-i]
	}

	n := 32
	if len(data) < 32 {
		n = len(data)
	}
	for i := 0; i < n; i++ {
		out[i] = data[i] ^ keyLE[i]
	}
	return out, nil
}

// LooksLikeKeyArtifact reports whether deobfuscated bytes are actually just the
// XOR key (which happens when the source odttf was empty/0 bytes): the result
// is exactly 32 bytes that are a 16-byte block repeated twice and is not a
// valid sfnt. This was the "bold font" red herring in the NEW_COASTER challenge.
func LooksLikeKeyArtifact(b []byte) bool {
	if len(b) != 32 || sfntMagic(b) {
		return false
	}
	for i := 0; i < 16; i++ {
		if b[i] != b[i+16] {
			return false
		}
	}
	return true
}
