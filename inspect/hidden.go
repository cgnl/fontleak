package inspect

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"

	"github.com/cgnl/fontleak/gsub"
	gtfont "github.com/go-text/typesetting/font"
)

// standardTags are the sfnt tables fontleak does not treat as "hidden".
var standardTags = map[string]bool{
	"GSUB": true, "GPOS": true, "GDEF": true, "BASE": true, "JSTF": true,
	"cmap": true, "glyf": true, "head": true, "hhea": true, "hmtx": true,
	"loca": true, "maxp": true, "name": true, "post": true, "OS/2": true,
	"cvt ": true, "fpgm": true, "prep": true, "gasp": true, "DSIG": true,
	"kern": true, "LTSH": true, "PCLT": true, "VDMX": true, "hdmx": true,
	"CFF ": true, "CFF2": true, "VORG": true, "STAT": true, "MATH": true,
	"avar": true, "fvar": true, "gvar": true, "HVAR": true, "MVAR": true,
	"VVAR": true, "COLR": true, "CPAL": true, "sbix": true, "SVG ": true,
	"meta": true, "vhea": true, "vmtx": true, "EBDT": true, "EBLC": true,
	"morx": true, "feat": true, "kerx": true, "trak": true, "ankr": true,
}

// Blob is a chunk of font bytes that may hide data (a non-standard table, or
// data trailing the last table), with any successful decode attempts.
type Blob struct {
	Origin  string
	Size    int
	Notes   []string // decode results (flag hits, printable text, inflated, font)
	Preview string
}

// HiddenData scans the raw sfnt for non-standard tables and trailing bytes, and
// tries to decode each (hex/base64/zlib) while looking for flag-shaped text.
func HiddenData(data []byte) []Blob {
	var blobs []Blob
	if len(data) < 12 {
		return nil
	}
	num := int(binary.BigEndian.Uint16(data[4:6]))
	if 12+num*16 > len(data) {
		return nil
	}
	var maxEnd uint32
	for i := 0; i < num; i++ {
		base := 12 + i*16
		tag := string(data[base : base+4])
		off := binary.BigEndian.Uint32(data[base+8 : base+12])
		length := binary.BigEndian.Uint32(data[base+12 : base+16])
		end := off + length
		if end > uint32(len(data)) {
			end = uint32(len(data))
		}
		if end > maxEnd {
			maxEnd = end
		}
		if !standardTags[tag] && off <= uint32(len(data)) {
			blobs = append(blobs, makeBlob(fmt.Sprintf("table %q", tag), data[off:end]))
		}
	}
	// Trailing data after the last table (4-byte alignment aside).
	if int(maxEnd) < len(data)-3 {
		blobs = append(blobs, makeBlob("trailing data after last table", data[maxEnd:]))
	}
	return blobs
}

func makeBlob(origin string, b []byte) Blob {
	blob := Blob{Origin: origin, Size: len(b)}
	blob.Preview = previewOf(b)
	blob.Notes = decodeAttempts(b)
	return blob
}

func previewOf(b []byte) string {
	n := len(b)
	if n > 48 {
		n = 48
	}
	var sb strings.Builder
	for _, c := range b[:n] {
		if c >= 0x20 && c < 0x7f {
			sb.WriteByte(c)
		} else {
			sb.WriteByte('.')
		}
	}
	return sb.String()
}

// decodeAttempts tries to surface hidden content: flag-shaped substrings in the
// raw bytes, and hex/base64/zlib decodings that yield printable text or a flag.
func decodeAttempts(b []byte) []string {
	var notes []string
	add := func(s string) {
		if s != "" && len(notes) < 8 {
			notes = append(notes, s)
		}
	}
	// Flag-shaped substrings in the raw bytes.
	for _, m := range flagRe.FindAll(b, -1) {
		add("flag-shaped: " + string(m))
	}
	txt := strings.TrimSpace(string(b))
	// hex
	if isAllHex(txt) && len(txt)%2 == 0 && len(txt) >= 8 {
		if dec, err := hex.DecodeString(txt); err == nil {
			if p, ok := printable(dec); ok {
				add("hex-decodes to: " + p)
			}
		}
	}
	// base64
	if looksBase64(txt) {
		if dec, err := base64.StdEncoding.DecodeString(txt); err == nil && len(dec) > 0 {
			if p, ok := printable(dec); ok {
				add("base64-decodes to: " + p)
			} else if isFont(dec) {
				add("base64-decodes to a FONT")
			}
		}
	}
	// zlib
	if zr, err := zlib.NewReader(bytes.NewReader(b)); err == nil {
		if dec, err := io.ReadAll(zr); err == nil && len(dec) > 0 {
			if p, ok := printable(dec); ok {
				add("zlib-inflates to: " + p)
			} else if isFont(dec) {
				add("zlib-inflates to a FONT")
			}
		}
	}
	return notes
}

func isAllHex(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func looksBase64(s string) bool {
	if len(s) < 8 || len(s)%4 != 0 {
		return false
	}
	for _, c := range s {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '=') {
			return false
		}
	}
	return true
}

func printable(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", false
	}
	n := 0
	for _, c := range b {
		if c >= 0x20 && c < 0x7f || c == '\n' || c == '\t' {
			n++
		}
	}
	if n*10 < len(b)*9 { // require >=90% printable
		return "", false
	}
	s := string(b)
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return strings.TrimSpace(s), true
}

func isFont(b []byte) bool {
	if len(b) < 4 {
		return false
	}
	switch string(b[:4]) {
	case "OTTO", "true", "ttcf", "wOFF", "wOF2":
		return true
	}
	return b[0] == 0 && b[1] == 1 && b[2] == 0 && b[3] == 0
}

// --- cmap / homoglyph text decoding ----------------------------------------

// outlineLetterMap maps each glyph to the ASCII letter/digit it visually draws.
// It anchors on glyph *names* (the post table), which a cmap-remap attack leaves
// intact, so it correctly reports that a remapped character draws something else.
// Outline matching then propagates the letter to renamed copies of a glyph.
func outlineLetterMap(face *gtfont.Face, rules *gsub.Rules) map[gsub.GID]rune {
	sigToChar := map[string]rune{}
	consider := func(g gsub.GID) {
		ch := letterFromGlyphName(face.Font.GlyphName(g))
		if ch == 0 {
			return
		}
		if s, ok := outlineSig(face, g); ok {
			if prev, ok := sigToChar[s]; !ok || ch < prev {
				sigToChar[s] = ch
			}
		}
	}
	// Anchor on every cmap'd glyph's name (covers all letters in normal fonts).
	for g := range rules.Glyph2Char {
		consider(g)
	}
	out := map[gsub.GID]rune{}
	for g := range rules.Glyph2Char {
		if s, ok := outlineSig(face, g); ok {
			if ch, ok := sigToChar[s]; ok {
				out[g] = ch
			}
		}
	}
	return out
}

// letterFromGlyphName returns the ASCII letter/digit a glyph name denotes:
// a bare single character ("A", "x", "5"), or a uniXXXX / uXXXXXX name.
func letterFromGlyphName(name string) rune {
	if len(name) == 1 {
		r := rune(name[0])
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return r
		}
	}
	if strings.HasPrefix(name, "uni") && len(name) == 7 {
		if v, ok := parseHex(name[3:]); ok && v < 128 {
			return rune(v)
		}
	}
	return 0
}

func parseHex(s string) (int, bool) {
	v := 0
	for _, c := range s {
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case c >= 'a' && c <= 'f':
			d = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = int(c-'A') + 10
		default:
			return 0, false
		}
		v = v*16 + d
	}
	return v, true
}

// CmapRemaps reports code points whose glyph visually draws a different
// character (homoglyph substitution: the text you see is not the bytes).
func CmapRemaps(face *gtfont.Face, rules *gsub.Rules) []string {
	draws := outlineLetterMap(face, rules)
	var out []string
	for g, cp := range rules.Glyph2Char {
		if cp >= 128 {
			continue
		}
		if d, ok := draws[g]; ok && d != cp {
			out = append(out, fmt.Sprintf("%q (U+%04X) actually draws %q", cp, cp, d))
		}
	}
	sort.Strings(out)
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

// DecodeRendered returns what the font visually renders for text, mapping each
// character's glyph to the letter that glyph draws. This reveals text hidden by
// cmap/homoglyph remapping (the visible string differs from the code points).
func DecodeRendered(face *gtfont.Face, rules *gsub.Rules, text string) string {
	draws := outlineLetterMap(face, rules)
	var sb strings.Builder
	for _, r := range text {
		g, ok := face.Font.NominalGlyph(r)
		if !ok {
			sb.WriteRune(r)
			continue
		}
		if d, ok := draws[gsub.GID(g)]; ok {
			sb.WriteRune(d)
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
