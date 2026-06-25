// Command fontleak analyses fonts (and the documents that embed them) for
// hidden data and CTF-style "logic" - flag checkers and ligature puzzles.
//
// Usage:
//
//	fontleak scan    <file>                       full pipeline + report
//	fontleak extract <doc>  [-o dir]              dump deobfuscated fonts
//	fontleak inspect <font>                       static analysis only
//	fontleak shape   <font> "text"                forward shaping oracle
//	fontleak solve   <font> [-mode auto|checker|ligature|feistel] [-alphabet hex]
//	fontleak verify  <font> "PVIB{...}"           does the font accept this input?
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cgnl/fontleak/extract"
	"github.com/cgnl/fontleak/gsub"
	"github.com/cgnl/fontleak/inspect"
	"github.com/cgnl/fontleak/oracle"
	"github.com/cgnl/fontleak/report"
	"github.com/cgnl/fontleak/sfnt"
	"github.com/cgnl/fontleak/solve"
	gtfont "github.com/go-text/typesetting/font"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	var err error
	switch cmd {
	case "scan":
		err = cmdScan(args)
	case "extract":
		err = cmdExtract(args)
	case "inspect":
		err = cmdInspect(args)
	case "shape":
		err = cmdShape(args)
	case "solve":
		err = cmdSolve(args)
	case "verify":
		err = cmdVerify(args)
	case "decode":
		err = cmdDecode(args)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `fontleak - find hidden data & CTF logic in fonts

  fontleak scan    <file>                 extract+normalize+inspect+solve, full report
  fontleak extract <doc> [-o dir]         dump deobfuscated embedded fonts
  fontleak inspect <font>                 static analysis (strings, GSUB, homoglyphs)
  fontleak shape   <font> "text"          forward shaping oracle (input -> glyphs)
  fontleak solve   <font> [-mode auto|checker|ligature|feistel] [-alphabet 0123456789abcdef]
  fontleak verify  <font> "PVIB{...}"     test whether the font accepts an input
  fontleak decode  <font> ["text"]        reveal cmap/homoglyph-hidden text (what the font really draws)
`)
}

// loadFontData reads a path and returns the most interesting recovered +
// normalized sfnt bytes. For documents with multiple fonts it picks the one
// with the most GSUB lookups (the "logic"-bearing font), falling back to the
// largest. A loose font file is returned as-is (normalized).
func loadFontData(path string) ([]byte, error) {
	fonts, err := extract.FromFile(path)
	if err != nil {
		return nil, err
	}
	var best []byte
	bestScore := -1
	for _, f := range fonts {
		if len(f.Data) == 0 {
			continue
		}
		norm, _, nerr := sfnt.Normalize(f.Data)
		if nerr != nil {
			norm = f.Data
		}
		// Score = GSUB lookups (logic), tie-broken by size.
		score := len(norm) / 1024 // baseline: size in KiB
		if face, perr := gtfont.ParseTTF(bytes.NewReader(norm)); perr == nil {
			score += 100000 * len(face.Font.GSUB.Lookups)
		}
		if score > bestScore {
			bestScore = score
			best = norm
		}
	}
	if len(best) == 0 {
		return nil, fmt.Errorf("no usable font data in %s", path)
	}
	return best, nil
}

func loadFace(path string) ([]byte, *gtfont.Face, *gsub.Rules, error) {
	data, err := loadFontData(path)
	if err != nil {
		return nil, nil, nil, err
	}
	face, err := gtfont.ParseTTF(bytes.NewReader(data))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse font: %w", err)
	}
	return data, face, gsub.Extract(face), nil
}

func cmdScan(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: fontleak scan <file>")
	}
	opt := report.DefaultOptions()
	rest := parseFlags(args, map[string]*string{"-alphabet": &opt.Alphabet})
	if len(rest) < 1 {
		return fmt.Errorf("usage: fontleak scan <file>")
	}
	out, err := report.Scan(rest[0], opt)
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

func cmdExtract(args []string) error {
	outDir := "."
	rest := parseFlags(args, map[string]*string{"-o": &outDir})
	if len(rest) < 1 {
		return fmt.Errorf("usage: fontleak extract <doc> [-o dir]")
	}
	fonts, err := extract.FromFile(rest[0])
	if err != nil {
		return err
	}
	n := 0
	for _, f := range fonts {
		if len(f.Data) == 0 {
			fmt.Printf("skip  %s [%s]\n", f.Name, f.Style)
			continue
		}
		name := sanitize(f.Name) + sanitize(f.Style) + ".ttf"
		p := filepath.Join(outDir, name)
		if err := os.WriteFile(p, f.Data, 0o644); err != nil {
			return err
		}
		fmt.Printf("wrote %s (%d bytes)\n", p, len(f.Data))
		n++
	}
	if n == 0 {
		fmt.Println("no usable fonts extracted")
	}
	return nil
}

func cmdInspect(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: fontleak inspect <font>")
	}
	data, face, rules, err := loadFace(args[0])
	if err != nil {
		return err
	}
	rep := inspect.Run(data, face, rules)
	fmt.Printf("GSUB: %d lookups, %d ligatures\n", rep.NumLookups, rep.NumLigatures)
	fmt.Printf("lookup kinds: %v\n", rep.LookupCounts)
	fmt.Printf("cmap entries: %d\n", rep.CmapEntries)
	if len(rep.FlagMatches) > 0 {
		fmt.Printf("flag-shaped strings:\n")
		for _, m := range rep.FlagMatches {
			fmt.Printf("  %s\n", m)
		}
	}
	if len(rep.Homoglyphs) > 0 {
		fmt.Printf("homoglyphs:\n")
		for _, h := range rep.Homoglyphs {
			fmt.Printf("  %s\n", h)
		}
	}
	if len(rep.Remaps) > 0 {
		fmt.Printf("cmap remaps (rendered text differs from code points):\n")
		for _, r := range rep.Remaps {
			fmt.Printf("  %s\n", r)
		}
	}
	for _, blob := range rep.Hidden {
		fmt.Printf("hidden blob: %s (%d bytes) preview=%q\n", blob.Origin, blob.Size, blob.Preview)
		for _, n := range blob.Notes {
			fmt.Printf("    %s\n", n)
		}
	}
	if len(rep.Strings) > 0 {
		fmt.Printf("notable strings:\n")
		for _, s := range rep.Strings {
			fmt.Printf("  %q\n", s)
		}
	}
	fmt.Println("verdict:")
	for _, v := range rep.Verdict {
		fmt.Printf("  • %s\n", v)
	}
	return nil
}

func cmdShape(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: fontleak shape <font> \"text\"")
	}
	data, err := loadFontData(args[0])
	if err != nil {
		return err
	}
	sh, err := oracle.New(data)
	if err != nil {
		return err
	}
	gids := sh.Shape(args[1])
	fmt.Printf("input:    %q\n", args[1])
	fmt.Printf("glyphs:   %s\n", strings.Join(sh.Names(gids), " "))
	fmt.Printf("readable: %q\n", sh.Readable(gids))
	return nil
}

func cmdSolve(args []string) error {
	mode := "auto"
	alphabet := "0123456789abcdef"
	rest := parseFlags(args, map[string]*string{"-mode": &mode, "-alphabet": &alphabet})
	if len(rest) < 1 {
		return fmt.Errorf("usage: fontleak solve <font> [-mode auto|checker|ligature|feistel] [-alphabet ...]")
	}
	data, face, rules, err := loadFace(rest[0])
	if err != nil {
		return err
	}
	sh, err := oracle.New(data)
	if err != nil {
		return err
	}

	if mode == "feistel" {
		res := solve.SolveFeistel(face, rules)
		if res.Found {
			fmt.Printf("★ SOLVED: %s\n  %s\n", res.Input, res.Note)
			return nil
		}
		fmt.Printf("feistel: %s\n", res.Note)
		return nil
	}

	if mode == "auto" || mode == "checker" {
		if rec, ok := solve.FindRecognition(rules); ok {
			fmt.Printf("checker: %s<secret>%s -> %q (secret length %d)\n", rec.Prefix, rec.Suffix, rec.Phrase, len(rec.Target))
			res := solve.SolveChecker(sh, rules, alphabet)
			if res.Found {
				fmt.Printf("★ SOLVED: %s\n  %s\n", res.Input, res.Note)
				return nil
			}
			fmt.Printf("  black-box: %s (oracle calls: %d)\n", res.Note, res.OracleCalls)
			// Fall back to white-box round inversion for strong round ciphers.
			if mode == "auto" {
				if fr := solve.SolveFeistel(face, rules); fr.Found {
					fmt.Printf("★ SOLVED (white-box round inversion): %s\n  %s\n", fr.Input, fr.Note)
					return nil
				} else {
					fmt.Printf("  white-box: %s\n", fr.Note)
				}
			}
		} else if mode == "checker" {
			fmt.Println("no checker recognition rule found")
		}
	}

	if mode == "auto" || mode == "ligature" {
		// Self-verifying recovery: a final form whose outline draws a readable
		// letter is the font's success signal. The rare letter is the flag.
		verified := solve.SolveLigatureVerified(face, rules, 16)
		if len(verified) > 0 {
			counts := solve.LetterCount(verified)
			top := verified[0]
			fmt.Printf("★ LIGATURE-COLLAPSE SOLVED: typing this makes the font draw %q (the success glyph):\n  %s\n",
				top.Draws, top.Expansion)
			fmt.Printf("  (%q is drawn by %d input(s); other letters are decoys)\n", top.Draws, counts[top.Draws])
			return nil
		}
		cands := solve.SolveLigature(rules, 8, sh.GlyphName)
		if len(cands) == 0 {
			if mode == "ligature" {
				fmt.Println("no ligature-collapse candidates found")
			}
			return nil
		}
		fmt.Printf("%d candidates; showing the longest hex ones (no single success glyph identified):\n", len(cands))
		for i, c := range cands {
			tag := ""
			if c.AllHex {
				tag = " [hex]"
			}
			fmt.Printf("ligature-candidate%s len=%d: %s\n", tag, c.Length, c.Expansion)
			if i >= 9 {
				break
			}
		}
	}
	return nil
}

func cmdVerify(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: fontleak verify <font> \"PVIB{...}\"")
	}
	data, err := loadFontData(args[0])
	if err != nil {
		return err
	}
	sh, err := oracle.New(data)
	if err != nil {
		return err
	}
	rendered, ok := solve.Verify(sh, args[1])
	fmt.Printf("input:    %q\n", args[1])
	fmt.Printf("renders:  %q\n", rendered)
	if ok {
		fmt.Println("result:   ✓ ACCEPTED (font renders a success phrase)")
	} else {
		fmt.Println("result:   ✗ rejected")
	}
	return nil
}

func cmdDecode(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: fontleak decode <font> [\"text\"]")
	}
	_, face, rules, err := loadFace(args[0])
	if err != nil {
		return err
	}
	if len(args) >= 2 {
		// Show what the font visually renders for the given text.
		rendered := inspect.DecodeRendered(face, rules, args[1])
		fmt.Printf("typed:    %q\n", args[1])
		fmt.Printf("rendered: %q\n", rendered)
		return nil
	}
	// No text: list the cmap remaps (characters that draw something else).
	remaps := inspect.CmapRemaps(face, rules)
	if len(remaps) == 0 {
		fmt.Println("no cmap/homoglyph remaps: the font draws characters as themselves")
		return nil
	}
	fmt.Printf("%d character(s) render as a different glyph:\n", len(remaps))
	for _, r := range remaps {
		fmt.Printf("  %s\n", r)
	}
	return nil
}

// parseFlags pulls "-flag value" pairs into the given string pointers and
// returns the remaining positional args. Minimal, dependency-free.
func parseFlags(args []string, flags map[string]*string) []string {
	var rest []string
	for i := 0; i < len(args); i++ {
		if p, ok := flags[args[i]]; ok && i+1 < len(args) {
			*p = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	return rest
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
