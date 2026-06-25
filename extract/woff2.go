package extract

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/andybalholm/brotli"
)

// woff2KnownTags maps the WOFF2 "known table" flag index (0..62) to its tag.
var woff2KnownTags = []string{
	"cmap", "head", "hhea", "hmtx", "maxp", "name", "OS/2", "post",
	"cvt ", "fpgm", "glyf", "loca", "prep", "CFF ", "VORG", "EBDT",
	"EBLC", "gasp", "hdmx", "kern", "LTSH", "PCLT", "VDMX", "vhea",
	"vmtx", "BASE", "GDEF", "GPOS", "GSUB", "EBSC", "JSTF", "MATH",
	"CBDT", "CBLC", "COLR", "CPAL", "SVG ", "sbix", "acnt", "avar",
	"bdat", "bloc", "bsln", "cvar", "fdsc", "feat", "fmtx", "fvar",
	"gvar", "hsty", "just", "lcar", "mort", "morx", "opbd", "prop",
	"trak", "Zapf", "Silf", "Glat", "Gloc", "Feat", "Sill",
}

func tagToUint32(s string) uint32 {
	var b [4]byte
	copy(b[:], s)
	for i := len(s); i < 4; i++ {
		b[i] = ' '
	}
	return binary.BigEndian.Uint32(b[:])
}

// decodeWOFF2 reconstructs a plain sfnt from a WOFF2 file. Non-glyf tables
// (including GSUB and cmap, where font CTF logic lives) are reconstructed
// exactly. A transformed glyf table is replaced with empty-but-valid glyf/loca
// so the font parses and shapes; glyph outlines are then unavailable.
func decodeWOFF2(data []byte) ([]byte, error) {
	if len(data) < 48 || string(data[:4]) != "wOF2" {
		return nil, fmt.Errorf("not a WOFF2 file")
	}
	flavor := binary.BigEndian.Uint32(data[4:8])
	numTables := int(binary.BigEndian.Uint16(data[12:14]))
	totalCompressed := binary.BigEndian.Uint32(data[20:24])

	r := &cursor{b: data, p: 48}
	type entry struct {
		tag             string
		transformed     bool
		origLength      uint32
		transformLength uint32
	}
	entries := make([]entry, numTables)
	for i := 0; i < numTables; i++ {
		flags, err := r.u8()
		if err != nil {
			return nil, err
		}
		idx := flags & 0x3f
		transformVersion := (flags >> 6) & 0x3
		var tag string
		if idx == 0x3f {
			t, err := r.bytes(4)
			if err != nil {
				return nil, err
			}
			tag = string(t)
		} else if int(idx) < len(woff2KnownTags) {
			tag = woff2KnownTags[idx]
		} else {
			return nil, fmt.Errorf("woff2: bad table index %d", idx)
		}
		origLen, err := r.base128()
		if err != nil {
			return nil, err
		}
		transformed := false
		if tag == "glyf" || tag == "loca" {
			transformed = transformVersion != 3
		} else {
			transformed = transformVersion != 0
		}
		var transformLen uint32
		if transformed {
			if transformLen, err = r.base128(); err != nil {
				return nil, err
			}
		}
		entries[i] = entry{tag, transformed, origLen, transformLen}
	}

	// Brotli-decompress the data block.
	comp := data[r.p:]
	if uint32(len(comp)) < totalCompressed {
		return nil, fmt.Errorf("woff2: truncated compressed block")
	}
	br := brotli.NewReader(bytes.NewReader(comp[:totalCompressed]))
	stream, err := io.ReadAll(br)
	if err != nil {
		return nil, fmt.Errorf("woff2: brotli: %w", err)
	}

	// Slice each table from the decompressed stream.
	var tables []namedTable
	var maxpData, headData []byte
	glyfTransformed := false
	sp := 0
	for _, e := range entries {
		n := int(e.origLength)
		if e.transformed {
			n = int(e.transformLength)
		}
		if sp+n > len(stream) {
			return nil, fmt.Errorf("woff2: table %q exceeds stream", e.tag)
		}
		raw := stream[sp : sp+n]
		sp += n
		switch {
		case e.tag == "glyf" && e.transformed:
			glyfTransformed = true // synthesised below
		case e.tag == "loca" && e.transformed:
			// reconstructed alongside glyf
		default:
			cp := append([]byte(nil), raw...)
			tables = append(tables, namedTable{tagToUint32(e.tag), cp})
			if e.tag == "maxp" {
				maxpData = cp
			}
			if e.tag == "head" {
				headData = cp
			}
		}
	}

	if glyfTransformed {
		glyf, loca, err := emptyGlyfLoca(maxpData, headData)
		if err != nil {
			return nil, err
		}
		tables = append(tables, namedTable{tagToUint32("glyf"), glyf})
		tables = append(tables, namedTable{tagToUint32("loca"), loca})
	}

	return sfntFromTables(flavor, tables), nil
}

// emptyGlyfLoca builds an empty glyf and a matching loca for numGlyphs glyphs.
func emptyGlyfLoca(maxp, head []byte) (glyf, loca []byte, err error) {
	if len(maxp) < 6 {
		return nil, nil, fmt.Errorf("woff2: missing/short maxp")
	}
	numGlyphs := int(binary.BigEndian.Uint16(maxp[4:6]))
	longLoca := true
	if len(head) >= 52 {
		longLoca = binary.BigEndian.Uint16(head[50:52]) == 1
	}
	glyf = nil // all glyphs empty
	if longLoca {
		loca = make([]byte, (numGlyphs+1)*4) // zeros
	} else {
		loca = make([]byte, (numGlyphs+1)*2)
	}
	return glyf, loca, nil
}

// cursor is a minimal big-endian byte reader.
type cursor struct {
	b []byte
	p int
}

func (c *cursor) u8() (byte, error) {
	if c.p >= len(c.b) {
		return 0, io.ErrUnexpectedEOF
	}
	v := c.b[c.p]
	c.p++
	return v, nil
}

func (c *cursor) bytes(n int) ([]byte, error) {
	if c.p+n > len(c.b) {
		return nil, io.ErrUnexpectedEOF
	}
	v := c.b[c.p : c.p+n]
	c.p += n
	return v, nil
}

// base128 reads a UIntBase128 (1..5 bytes, 7 bits each, MSB = continuation).
func (c *cursor) base128() (uint32, error) {
	var v uint32
	for i := 0; i < 5; i++ {
		b, err := c.u8()
		if err != nil {
			return 0, err
		}
		v = v<<7 | uint32(b&0x7f)
		if b&0x80 == 0 {
			return v, nil
		}
	}
	return 0, fmt.Errorf("woff2: overlong UIntBase128")
}
