package solve

import (
	"unicode"

	"github.com/cgnl/fontleak/gsub"
	"github.com/cgnl/fontleak/sim"
	gtfont "github.com/go-text/typesetting/font"
)

// SolveFeistel recovers the secret from a checker font whose cipher is a
// round-based (Feistel/SPN) construction in GSUB. It uses the lookup-by-lookup
// simulator: the full cipher is full-diffusion, but each round mixes only
// neighbouring positions, so the rounds are inverted one at a time and chained.
// This solves strong ciphers that black-box search cannot.
func SolveFeistel(face *gtfont.Face, r *gsub.Rules) CheckerResult {
	rec, ok := FindRecognition(r)
	if !ok {
		return CheckerResult{Note: "no recognition rule found"}
	}
	res := CheckerResult{
		Detected:  true,
		Prefix:    rec.Prefix,
		Suffix:    rec.Suffix,
		Phrase:    rec.Phrase,
		TargetLen: len(rec.Target),
	}

	e := sim.New(face, "rlig")
	target := make([]sim.GID, len(rec.Target))
	for i, g := range rec.Target {
		target[i] = sim.GID(g)
	}

	secret, ok := e.Invert(target)
	if !ok {
		res.Note = "white-box round inversion did not converge (cipher may not be a local-diffusion round cipher)"
		return res
	}
	res.Solution = secret
	res.Input = rec.Prefix + secret + rec.Suffix

	// Verify by re-running the cipher forward: the recovered input must collapse
	// (the recognition rule fires), producing fewer glyphs than the cipher output.
	out := e.Forward(e.Glyphs(res.Input))
	if len(out) < len(rec.Target) {
		res.Found = true
		res.Note = "verified by white-box round inversion (recovered input collapses to the success phrase)"
		if ascii, ok := hexToPrintableASCII(secret); ok {
			res.Note += "; secret decodes to ASCII " + quote(ascii)
		}
	} else {
		res.Note = "inversion produced a candidate but it did not collapse on re-check"
	}
	return res
}

// hexToPrintableASCII decodes a hex string to ASCII if every byte is printable.
func hexToPrintableASCII(h string) (string, bool) {
	if len(h)%2 != 0 {
		return "", false
	}
	b := make([]byte, len(h)/2)
	for i := 0; i < len(b); i++ {
		hi, lo := hexNibble(h[2*i]), hexNibble(h[2*i+1])
		if hi < 0 || lo < 0 {
			return "", false
		}
		b[i] = byte(hi<<4 | lo)
		if !unicode.IsPrint(rune(b[i])) {
			return "", false
		}
	}
	return string(b), true
}

func hexNibble(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}
