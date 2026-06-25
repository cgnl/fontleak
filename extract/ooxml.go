package extract

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
)

// embedRef is one <w:embed*> reference: its style, relationship id and key.
type embedRef struct {
	Style   string // Regular | Bold | Italic | BoldItalic
	ID      string // r:id
	FontKey string // w:fontKey
}

// fontEntry is one <w:font> with its embedded references.
type fontEntry struct {
	Name   string
	Embeds []embedRef
}

// FromFile loads a path and returns the recovered fonts. It auto-detects an
// OOXML container (zip) versus a loose sfnt font file.
func FromFile(p string) ([]Font, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	if sfntMagic(data) {
		style := "Regular"
		return []Font{{Name: strings.TrimSuffix(path.Base(p), path.Ext(p)), Style: style, Source: p, Data: data}}, nil
	}
	// Try as a zip container (docx/pptx/xlsx are zips).
	if len(data) >= 2 && data[0] == 'P' && data[1] == 'K' {
		fonts, err := FromOOXML(data, p)
		if err != nil {
			return nil, err
		}
		return fonts, nil
	}
	return nil, fmt.Errorf("%s: not an sfnt font nor a zip/OOXML container", p)
}

// FromOOXML extracts and deobfuscates every embedded font referenced by a
// fontTable.xml inside the given OOXML (zip) bytes. Empty/key-artifact fonts
// (the "bold red herring") are reported with empty Data and a note in Style.
func FromOOXML(data []byte, source string) ([]Font, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}

	files := map[string]*zip.File{}
	var fontTablePath string
	for _, f := range zr.File {
		files[f.Name] = f
		if strings.HasSuffix(f.Name, "fontTable.xml") {
			fontTablePath = f.Name
		}
	}
	if fontTablePath == "" {
		return nil, fmt.Errorf("%s: no fontTable.xml (no embedded fonts?)", source)
	}

	ftBytes, err := readZip(files[fontTablePath])
	if err != nil {
		return nil, err
	}
	entries, err := parseFontTable(ftBytes)
	if err != nil {
		return nil, err
	}

	// rels: <dir>/_rels/<base>.rels next to the fontTable.
	dir := path.Dir(fontTablePath)
	relsPath := path.Join(dir, "_rels", path.Base(fontTablePath)+".rels")
	rid2target := map[string]string{}
	if rf := files[relsPath]; rf != nil {
		if rb, err := readZip(rf); err == nil {
			rid2target = parseRels(rb)
		}
	}

	var out []Font
	for _, f := range entries {
		for _, ref := range f.Embeds {
			target := rid2target[ref.ID]
			if target == "" {
				continue
			}
			zipPath := path.Join(dir, target)
			zf := files[zipPath]
			if zf == nil {
				// Some producers use a flat fonts/ path.
				zf = files[path.Join("word", target)]
			}
			if zf == nil {
				continue
			}
			raw, err := readZip(zf)
			if err != nil {
				continue
			}
			fnt := Font{Name: f.Name, Style: ref.Style, Source: source + "!" + zipPath}
			if len(raw) == 0 {
				fnt.Style = ref.Style + " (empty/0-byte — skipped)"
				out = append(out, fnt)
				continue
			}
			deob, err := Deobfuscate(raw, ref.FontKey)
			if err != nil {
				fnt.Style = ref.Style + " (deobfuscate failed: " + err.Error() + ")"
				out = append(out, fnt)
				continue
			}
			if LooksLikeKeyArtifact(deob) {
				fnt.Style = ref.Style + " (empty — only XOR-key artifact)"
				out = append(out, fnt)
				continue
			}
			fnt.Data = deob
			out = append(out, fnt)
		}
	}
	// Stable order: real fonts first, then by name/style.
	sort.SliceStable(out, func(i, j int) bool {
		if (len(out[i].Data) > 0) != (len(out[j].Data) > 0) {
			return len(out[i].Data) > 0
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Style < out[j].Style
	})
	return out, nil
}

// attr returns the value of the attribute with the given local name,
// ignoring any namespace prefix.
func attr(attrs []xml.Attr, local string) string {
	for _, a := range attrs {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

// parseFontTable walks word/fontTable.xml by local element names, so it is
// immune to namespace-prefix variations across producers. It collects each
// <w:font> with its <w:embed{Regular,Bold,Italic,BoldItalic}> references.
func parseFontTable(b []byte) ([]fontEntry, error) {
	dec := xml.NewDecoder(bytes.NewReader(b))
	var entries []fontEntry
	var cur *fontEntry
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse fontTable.xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch {
			case t.Name.Local == "font":
				entries = append(entries, fontEntry{Name: attr(t.Attr, "name")})
				cur = &entries[len(entries)-1]
			case strings.HasPrefix(t.Name.Local, "embed") && cur != nil:
				style := strings.TrimPrefix(t.Name.Local, "embed") // Regular/Bold/...
				cur.Embeds = append(cur.Embeds, embedRef{
					Style:   style,
					ID:      attr(t.Attr, "id"),
					FontKey: attr(t.Attr, "fontKey"),
				})
			}
		case xml.EndElement:
			if t.Name.Local == "font" {
				cur = nil
			}
		}
	}
	return entries, nil
}

// parseRels walks a .rels part and maps relationship Id -> Target.
func parseRels(b []byte) map[string]string {
	out := map[string]string{}
	dec := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "Relationship" {
			id := attr(se.Attr, "Id")
			target := attr(se.Attr, "Target")
			if id != "" {
				out[id] = target
			}
		}
	}
	return out
}

func readZip(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
