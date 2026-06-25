// Package sfnt repairs obfuscated sfnt (TrueType/OpenType) table directories.
// CTF fonts often rename layout tables (e.g. GSUB -> "ykm|", GDEF -> "zkqy")
// and falsify their lengths so normal parsers refuse the file. Normalize
// detects layout tables by their *content* and rewrites a clean, parseable sfnt.
package sfnt

import (
	"encoding/binary"
	"fmt"
	"sort"
)

// Change records one normalization action for reporting.
type Change struct {
	OldTag    string
	NewTag    string
	OldLength uint32
	NewLength uint32
}

// Report summarizes what Normalize did.
type Report struct {
	Changes   []Change
	NumTables int
}

func (r Report) Modified() bool { return len(r.Changes) > 0 }

type record struct {
	tag      [4]byte
	checksum uint32
	offset   uint32
	length   uint32
}

// knownTags are standard sfnt tables we never reinterpret.
var knownTags = map[string]bool{
	"GSUB": true, "GPOS": true, "GDEF": true, "BASE": true, "JSTF": true,
	"cmap": true, "glyf": true, "head": true, "hhea": true, "hmtx": true,
	"loca": true, "maxp": true, "name": true, "post": true, "OS/2": true,
	"cvt ": true, "fpgm": true, "prep": true, "gasp": true, "DSIG": true,
	"kern": true, "LTSH": true, "PCLT": true, "VDMX": true, "hdmx": true,
	"CFF ": true, "CFF2": true, "VORG": true, "EBDT": true, "EBLC": true,
	"EBSC": true, "GASP": true, "vhea": true, "vmtx": true, "STAT": true,
	"MATH": true, "avar": true, "fvar": true, "gvar": true, "HVAR": true,
	"MVAR": true, "VVAR": true, "COLR": true, "CPAL": true, "sbix": true,
	"meta": true, "SVG ": true, "bdat": true, "bloc": true, "morx": true,
	"feat": true, "ankr": true, "kerx": true, "trak": true, "Zapf": true,
}

func tagString(t [4]byte) string { return string(t[:]) }

// Normalize rewrites the sfnt so that obfuscated layout tables get their real
// tags back and every table length is corrected. Table *data* is left in place;
// only the directory (tags, lengths, order, checksums) is rebuilt.
func Normalize(data []byte) ([]byte, Report, error) {
	var rep Report
	if len(data) < 12 {
		return nil, rep, fmt.Errorf("not an sfnt: too short")
	}
	numTables := int(binary.BigEndian.Uint16(data[4:6]))
	rep.NumTables = numTables
	if 12+numTables*16 > len(data) {
		return nil, rep, fmt.Errorf("not an sfnt: directory exceeds file size")
	}

	recs := make([]record, numTables)
	for i := 0; i < numTables; i++ {
		base := 12 + i*16
		copy(recs[i].tag[:], data[base:base+4])
		recs[i].checksum = binary.BigEndian.Uint32(data[base+4 : base+8])
		recs[i].offset = binary.BigEndian.Uint32(data[base+8 : base+12])
		recs[i].length = binary.BigEndian.Uint32(data[base+12 : base+16])
	}

	// True lengths: distance to the next table by offset, clamped to EOF.
	byOff := make([]int, numTables)
	for i := range byOff {
		byOff[i] = i
	}
	sort.Slice(byOff, func(a, b int) bool { return recs[byOff[a]].offset < recs[byOff[b]].offset })
	trueLen := make([]uint32, numTables)
	for k, idx := range byOff {
		start := recs[idx].offset
		var end uint32
		if k+1 < len(byOff) {
			end = recs[byOff[k+1]].offset
		} else {
			end = uint32(len(data))
		}
		if start > uint32(len(data)) {
			start = uint32(len(data))
		}
		if end < start {
			end = start
		}
		trueLen[idx] = end - start
	}

	// Detect & rename obfuscated layout tables by their content.
	haveGSUB, haveGPOS, haveGDEF := false, false, false
	for i := range recs {
		switch tagString(recs[i].tag) {
		case "GSUB":
			haveGSUB = true
		case "GPOS":
			haveGPOS = true
		case "GDEF":
			haveGDEF = true
		}
	}

	for i := range recs {
		tag := tagString(recs[i].tag)
		oldLen := recs[i].length
		recs[i].length = trueLen[i]

		if knownTags[tag] {
			if oldLen != recs[i].length {
				rep.Changes = append(rep.Changes, Change{tag, tag, oldLen, recs[i].length})
			}
			continue
		}

		td := tableData(data, recs[i].offset, trueLen[i])
		var newTag string
		switch {
		case looksLikeLayoutWithScripts(td):
			if !haveGSUB {
				newTag, haveGSUB = "GSUB", true
			} else if !haveGPOS {
				newTag, haveGPOS = "GPOS", true
			}
		case looksLikeGDEF(td):
			if !haveGDEF {
				newTag, haveGDEF = "GDEF", true
			}
		}
		if newTag != "" {
			rep.Changes = append(rep.Changes, Change{tag, newTag, oldLen, recs[i].length})
			copy(recs[i].tag[:], newTag)
		} else if oldLen != recs[i].length {
			rep.Changes = append(rep.Changes, Change{tag, tag, oldLen, recs[i].length})
		}
	}

	out := rebuild(data, recs)
	return out, rep, nil
}

// tableData returns the slice for a table, clamped to the file bounds.
func tableData(data []byte, off, length uint32) []byte {
	if off > uint32(len(data)) {
		return nil
	}
	end := off + length
	if end > uint32(len(data)) {
		end = uint32(len(data))
	}
	return data[off:end]
}

// looksLikeLayoutWithScripts reports whether td is a GSUB/GPOS table: a 1.0/1.1
// header whose ScriptList offset points at a plausible ScriptList (sane count,
// ASCII script tags such as DFLT/latn). This distinguishes GSUB/GPOS from GDEF
// (which has no ScriptList).
func looksLikeLayoutWithScripts(td []byte) bool {
	if len(td) < 10 {
		return false
	}
	ver := binary.BigEndian.Uint32(td[0:4])
	if ver != 0x00010000 && ver != 0x00010001 {
		return false
	}
	scriptListOff := int(binary.BigEndian.Uint16(td[4:6]))
	if scriptListOff < 10 || scriptListOff+2 > len(td) {
		return false
	}
	count := int(binary.BigEndian.Uint16(td[scriptListOff : scriptListOff+2]))
	if count < 1 || count > 256 {
		return false
	}
	// Inspect the first script record tag.
	rec := scriptListOff + 2
	if rec+6 > len(td) {
		return false
	}
	for i := 0; i < 4; i++ {
		c := td[rec+i]
		if !(c == ' ' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// looksLikeGDEF reports whether td resembles a GDEF table header.
func looksLikeGDEF(td []byte) bool {
	if len(td) < 12 {
		return false
	}
	ver := binary.BigEndian.Uint32(td[0:4])
	switch ver {
	case 0x00010000, 0x00010002, 0x00010003:
	default:
		return false
	}
	// First offset (glyphClassDef) should be within the table or zero, and the
	// table should NOT parse as a ScriptList-bearing layout table.
	if looksLikeLayoutWithScripts(td) {
		return false
	}
	first := int(binary.BigEndian.Uint16(td[4:6]))
	return first == 0 || first < len(td)
}

// rebuild writes a fresh sfnt: recomputed header + directory (sorted by tag,
// per-table checksums and head.checkSumAdjustment fixed), with the original
// table data region appended unchanged (offsets preserved).
func rebuild(orig []byte, recs []record) []byte {
	// Recompute per-table checksums from the (now correct) data + length.
	for i := range recs {
		recs[i].checksum = tableChecksum(orig, recs[i].offset, recs[i].length)
	}
	// Records must be sorted by tag per the OpenType spec.
	sort.Slice(recs, func(a, b int) bool { return tagString(recs[a].tag) < tagString(recs[b].tag) })

	out := make([]byte, len(orig))
	copy(out, orig)

	// Header.
	n := len(recs)
	binary.BigEndian.PutUint16(out[4:6], uint16(n))
	entrySelector := uint16(0)
	for 1<<(entrySelector+1) <= n {
		entrySelector++
	}
	searchRange := uint16(16) << entrySelector
	binary.BigEndian.PutUint16(out[6:8], searchRange)
	binary.BigEndian.PutUint16(out[8:10], entrySelector)
	binary.BigEndian.PutUint16(out[10:12], uint16(n*16)-searchRange)

	// Directory.
	for i, r := range recs {
		base := 12 + i*16
		copy(out[base:base+4], r.tag[:])
		binary.BigEndian.PutUint32(out[base+4:base+8], r.checksum)
		binary.BigEndian.PutUint32(out[base+8:base+12], r.offset)
		binary.BigEndian.PutUint32(out[base+12:base+16], r.length)
	}

	// head.checkSumAdjustment = 0xB1B0AFBA - checksum(entire font with field=0).
	for _, r := range recs {
		if tagString(r.tag) == "head" && r.length >= 12 && r.offset+12 <= uint32(len(out)) {
			adjPos := r.offset + 8
			binary.BigEndian.PutUint32(out[adjPos:adjPos+4], 0)
			total := checksumBytes(out)
			binary.BigEndian.PutUint32(out[adjPos:adjPos+4], 0xB1B0AFBA-total)
			break
		}
	}
	return out
}

// tableChecksum sums the table's 32-bit big-endian words (padded with zeros).
func tableChecksum(data []byte, off, length uint32) uint32 {
	return checksumBytes(tableData(data, off, length))
}

func checksumBytes(b []byte) uint32 {
	var sum uint32
	var word uint32
	for i := 0; i < len(b); i++ {
		word = word<<8 | uint32(b[i])
		if i%4 == 3 {
			sum += word
			word = 0
		}
	}
	if r := len(b) % 4; r != 0 {
		word <<= uint(8 * (4 - r)) // pad remaining bytes with zeros
		sum += word
	}
	return sum
}
