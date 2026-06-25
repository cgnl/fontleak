package extract

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"regexp"
	"sort"

	"github.com/go-text/typesetting/font/opentype"
)

// inflate decompresses a zlib (PDF FlateDecode) or raw-deflate stream.
func inflate(b []byte) ([]byte, error) {
	if zr, err := zlib.NewReader(bytes.NewReader(b)); err == nil {
		if out, err := io.ReadAll(zr); err == nil && len(out) > 0 {
			return out, nil
		}
	}
	fr := flate.NewReader(bytes.NewReader(b))
	out, err := io.ReadAll(fr)
	if err != nil || len(out) == 0 {
		return nil, fmt.Errorf("not a deflate stream")
	}
	return out, nil
}

// DecodeToSFNT turns font-container bytes into plain sfnt (TrueType/OpenType)
// bytes that the rest of the pipeline understands. It accepts raw sfnt, WOFF,
// WOFF2, TrueType collections and dfont. WOFF2 is decoded internally; the others
// go through go-text's loader and are re-emitted as a standard sfnt.
func DecodeToSFNT(data []byte) ([]byte, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("too short to be a font")
	}
	switch string(data[:4]) {
	case "wOF2":
		return decodeWOFF2(data)
	}
	if sfntMagic(data) {
		return data, nil // already plain sfnt
	}
	// WOFF1, ttcf, dfont, OTTO/true: let go-text parse and re-emit.
	ld, err := opentype.NewLoader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("unrecognised font container: %w", err)
	}
	return sfntFromLoader(ld)
}

// namedTable is a tag plus its raw bytes.
type namedTable struct {
	tag  uint32
	data []byte
}

// sfntFromLoader reassembles a standard sfnt file from a parsed loader's tables.
func sfntFromLoader(ld *opentype.Loader) ([]byte, error) {
	var tbls []namedTable
	for _, t := range ld.Tables() {
		d, err := ld.RawTable(t)
		if err != nil {
			continue
		}
		tbls = append(tbls, namedTable{uint32(t), d})
	}
	if len(tbls) == 0 {
		return nil, fmt.Errorf("loader exposed no tables")
	}
	return sfntFromTables(uint32(ld.Type), tbls), nil
}

// sfntFromTables assembles a standard sfnt from (tag, data) pairs.
func sfntFromTables(sfntVersion uint32, tbls []namedTable) []byte {
	sort.Slice(tbls, func(i, j int) bool { return tbls[i].tag < tbls[j].tag })
	n := len(tbls)
	offsets := make([]uint32, n)
	pos := uint32(12 + n*16)
	for i := range tbls {
		offsets[i] = pos
		pos += uint32(len(tbls[i].data))
		pos = (pos + 3) &^ 3
	}
	out := make([]byte, pos)
	binary.BigEndian.PutUint32(out[0:4], sfntVersion)
	binary.BigEndian.PutUint16(out[4:6], uint16(n))
	es := uint16(0)
	for 1<<(es+1) <= n {
		es++
	}
	sr := uint16(16) << es
	binary.BigEndian.PutUint16(out[6:8], sr)
	binary.BigEndian.PutUint16(out[8:10], es)
	binary.BigEndian.PutUint16(out[10:12], uint16(n*16)-sr)
	for i, t := range tbls {
		rec := 12 + i*16
		binary.BigEndian.PutUint32(out[rec:rec+4], t.tag)
		binary.BigEndian.PutUint32(out[rec+4:rec+8], tableChecksum(t.data))
		binary.BigEndian.PutUint32(out[rec+8:rec+12], offsets[i])
		binary.BigEndian.PutUint32(out[rec+12:rec+16], uint32(len(t.data)))
		copy(out[offsets[i]:], t.data)
	}
	return out
}

func tableChecksum(b []byte) uint32 {
	var sum, word uint32
	for i := 0; i < len(b); i++ {
		word = word<<8 | uint32(b[i])
		if i%4 == 3 {
			sum += word
			word = 0
		}
	}
	if r := len(b) % 4; r != 0 {
		word <<= uint(8 * (4 - r))
		sum += word
	}
	return sum
}

// dataURIRe matches a base64 font payload inside a data: URI (CSS/HTML/SVG).
var dataURIRe = regexp.MustCompile(`data:[^;,]*(?:font|octet-stream|sfnt|woff2?|truetype|opentype)[^;,]*;base64,([A-Za-z0-9+/=\s]+)`)

// fontsFromText extracts base64 data: URI fonts from CSS/HTML/SVG text.
func fontsFromText(data []byte, source string) ([]Font, error) {
	var out []Font
	for i, m := range dataURIRe.FindAllSubmatch(data, -1) {
		raw, err := base64.StdEncoding.DecodeString(stripSpace(string(m[1])))
		if err != nil {
			continue
		}
		sf, err := DecodeToSFNT(raw)
		if err != nil {
			continue
		}
		out = append(out, Font{
			Name:   fmt.Sprintf("data-uri-font-%d", i+1),
			Style:  "Regular",
			Source: source + " (data: URI)",
			Data:   sf,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no base64 data: URI fonts found")
	}
	return out, nil
}

func stripSpace(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		if c := s[i]; c != '\n' && c != '\r' && c != '\t' && c != ' ' {
			b = append(b, c)
		}
	}
	return string(b)
}

// streamRe matches any PDF stream body. Embedded font programs (FontFile,
// FontFile2, FontFile3) are referenced from a FontDescriptor, not labelled on
// the stream object itself, so we inflate every stream and keep the ones that
// decode to a font.
var streamRe = regexp.MustCompile(`(?s)stream\r?\n(.*?)endstream`)

// fontsFromPDF extracts and inflates embedded font programs from a PDF.
func fontsFromPDF(data []byte, source string) ([]Font, error) {
	var out []Font
	seen := map[string]bool{}
	for i, m := range streamRe.FindAllSubmatch(data, -1) {
		raw := bytes.TrimRight(m[1], "\r\n")
		candidates := [][]byte{raw}
		if dec, err := inflate(raw); err == nil {
			candidates = append([][]byte{dec}, candidates...)
		}
		for _, cand := range candidates {
			sf, err := DecodeToSFNT(cand)
			if err != nil {
				continue
			}
			key := fmt.Sprintf("%d:%x", len(sf), sf[:min(16, len(sf))])
			if seen[key] {
				break
			}
			seen[key] = true
			out = append(out, Font{
				Name:   fmt.Sprintf("pdf-font-%d", i+1),
				Style:  "Regular",
				Source: source + " (PDF embedded)",
				Data:   sf,
			})
			break
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no usable embedded fonts found in PDF")
	}
	return out, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
