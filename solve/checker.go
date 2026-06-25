// Package solve recovers hidden inputs from "logic" fonts. It implements two
// strategies:
//
//   - checker inversion (this file): the font validates a typed string and only
//     renders a readable success phrase ("Your Flag Is Correct") when the exact
//     input is given. We find the recognition rule, read the target glyph
//     sequence it expects, and invert the (possibly avalanche) cipher using the
//     shaping oracle as a black box. This solves the NEW_COASTER challenge.
//
//   - ligature expansion (ligature.go): the font collapses a typed string into a
//     single glyph; we reverse the ligature chains to recover the input. This
//     solves the FONT LEAGUES challenge.
package solve

import (
	"fmt"
	"strings"

	"github.com/cgnl/fontleak/gsub"
	"github.com/cgnl/fontleak/oracle"
)

// successKeywords flag a recognition rule's readable output as a "you win" phrase.
var successKeywords = []string{"correct", "flag", "congrat", "you win", "you got", "valid", "success", "well done", "nice", "right"}

// CheckerResult is the outcome of an inversion attempt.
type CheckerResult struct {
	Found       bool
	Detected    bool   // a recognition rule was found (even if not inverted)
	Prefix      string // literal frame before the secret, e.g. "PVIB{"
	Suffix      string // literal frame after, e.g. "}"
	Solution    string // the recovered secret (between prefix/suffix)
	Input       string // full input that triggers success: Prefix+Solution+Suffix
	Phrase      string // the success phrase the font renders for a correct input
	TargetLen   int    // number of secret symbols the rule expects
	Alphabet    string
	OracleCalls int
	Note        string
}

// Recognition describes a found "you win" rule.
type Recognition struct {
	Prefix       string
	Suffix       string
	PrefixGlyphs int        // glyph count of the literal prefix run
	SuffixGlyphs int        // glyph count of the literal suffix run
	Target       []gsub.GID // the cipher-output glyphs the secret must produce
	Phrase       string
}

// FindRecognition scans the ligatures for one whose output expands to a
// human-readable success phrase, and splits its components into a literal frame
// plus the target (cipher) glyph sequence.
func FindRecognition(r *gsub.Rules) (Recognition, bool) {
	var best Recognition
	bestScore := -1
	for _, lig := range r.Ligatures {
		phrase := expandReadable(r, lig.Out, 0)
		if phrase == "" {
			continue
		}
		score := readableScore(phrase)
		hasKW := containsKeyword(phrase)
		// Require a phrase that is mostly letters/spaces and reasonably long,
		// strongly preferring ones with a success keyword.
		if score < 6 {
			continue
		}
		rank := score
		if hasKW {
			rank += 1000
		}
		if rank <= bestScore {
			continue
		}

		prefixChars, prefixN := leadingReadable(r, lig.Components)
		suffixChars, suffixN := trailingReadable(r, lig.Components, prefixN)
		if prefixN+suffixN >= len(lig.Components) {
			continue // no secret region
		}
		best = Recognition{
			Prefix:       prefixChars,
			Suffix:       suffixChars,
			PrefixGlyphs: prefixN,
			SuffixGlyphs: suffixN,
			Target:       append([]gsub.GID(nil), lig.Components[prefixN:len(lig.Components)-suffixN]...),
			Phrase:       phrase,
		}
		bestScore = rank
	}
	return best, bestScore >= 0
}

// SolveChecker finds the recognition rule and inverts the cipher to recover the
// secret. alphabet is the candidate symbol set for the secret (default hex).
func SolveChecker(sh *oracle.Shaper, r *gsub.Rules, alphabet string) CheckerResult {
	if alphabet == "" {
		alphabet = "0123456789abcdef"
	}
	rec, ok := FindRecognition(r)
	if !ok {
		return CheckerResult{Note: "no recognition rule (readable success phrase) found in GSUB"}
	}
	res := CheckerResult{
		Detected:  true,
		Prefix:    rec.Prefix,
		Suffix:    rec.Suffix,
		Phrase:    rec.Phrase,
		TargetLen: len(rec.Target),
		Alphabet:  alphabet,
	}

	inv := &inverter{
		shape:    sh.Shape,
		prefix:   rec.Prefix,
		suffix:   rec.Suffix,
		preN:     rec.PrefixGlyphs,
		sufN:     rec.SuffixGlyphs,
		target:   rec.Target,
		alphabet: []rune(alphabet),
	}
	sol, ok := inv.solve()
	res.OracleCalls = inv.calls
	if !ok {
		res.Solution = sol // partial
		res.Input = rec.Prefix + sol + rec.Suffix
		res.Note = "recognition rule found but black-box inversion did not converge " +
			"(strong/full-diffusion cipher). Target extracted; use Verify to test candidates."
		return res
	}

	// Verify end-to-end: the recovered input must render the success phrase.
	input := rec.Prefix + sol + rec.Suffix
	got := sh.ShapeText(input)
	res.Solution = sol
	res.Input = input
	if readableContainsAny(got, successKeywords) || normalize(got) == normalize(rec.Phrase) {
		res.Found = true
		res.Note = "verified: font self-check renders " + quote(strings.TrimRight(got, "."))
	} else {
		res.Note = "inversion converged but self-check did not confirm (rendered " + quote(got) + ")"
	}
	return res
}

// Verify reports whether typing input into the font renders a success phrase
// (the font's own self-check), returning the rendered text.
func Verify(sh *oracle.Shaper, input string) (rendered string, success bool) {
	rendered = sh.ShapeText(input)
	return rendered, readableContainsAny(rendered, successKeywords)
}

// Hints returns actionable next-steps when a checker was detected but the secret
// could not be recovered by black-box inversion. They are deliberately neutral:
// they describe what the structure implies and where additional data *might*
// live, without asserting any particular resource is required.
func Hints(rec Recognition, alphabet string, emptyCompanions []string) []string {
	var h []string
	n := len(rec.Target)
	h = append(h, fmt.Sprintf("Secret is %d symbol(s) over alphabet %q; confirm any guess with `verify`.", n, alphabet))
	if alphabet == "0123456789abcdef" && n%2 == 0 {
		h = append(h, fmt.Sprintf("That is %d hex digits = %d bytes - if recovered, try decoding hex→ASCII (flags sometimes spell words).", n, n/2))
	}
	h = append(h, "The cipher is strong/full-diffusion: black-box search can't invert it. "+
		"Recover it by analysing the GSUB round structure (per-round S-boxes) statically, "+
		"then inverting round-by-round.")
	if len(emptyCompanions) > 0 {
		h = append(h, "Companion font reference(s) are empty/missing here: "+strings.Join(emptyCompanions, ", ")+
			". Usually a red herring, but some challenges split a key or second stage across styles - "+
			"check whether yours should carry data (this file does not).")
	}
	return h
}

// inverter inverts a (possibly triangular/avalanche) cipher using a shaping
// function as a black-box oracle: it finds the secret X such that shaping
// prefix+X+suffix produces the target glyph sequence in the middle. The shape
// dependency is injected (not the concrete Shaper) so the solver is unit-testable
// against synthetic ciphers.
type inverter struct {
	shape          func(string) []gsub.GID
	prefix, suffix string
	preN, sufN     int
	target         []gsub.GID
	alphabet       []rune
	calls          int
}

// middle shapes prefix+X+suffix and returns the glyphs between the frame.
func (in *inverter) middle(x []rune) []gsub.GID {
	in.calls++
	out := in.shape(in.prefix + string(x) + in.suffix)
	lo, hi := in.preN, len(out)-in.sufN
	if lo < 0 || hi > len(out) || lo > hi {
		return nil
	}
	return out[lo:hi]
}

// maxGroup bounds how many inputs we brute-force jointly for one cipher stage.
// 2 is enough for a Feistel seed; 3 is a safety margin. Larger => abort.
const maxGroup = 3

// callBudget caps oracle shapes to keep a pathological font from hanging.
const callBudget = 4_000_000

func (in *inverter) solve() (string, bool) {
	n := len(in.target) // 1 secret symbol per target glyph (1:1 cipher)
	x := make([]rune, n)
	for i := range x {
		x[i] = in.alphabet[0]
	}
	if len(in.middle(x)) != n {
		return string(x), false // ratio not 1:1; report partial
	}

	controllers := in.probeControllers(x, n)
	groups, checks, ok := planOrder(controllers, n)
	if !ok {
		return string(x), false // too diffuse to invert with small groups
	}

	var dfs func(gi int) bool
	dfs = func(gi int) bool {
		if in.calls > callBudget {
			return false
		}
		if gi == len(groups) {
			m := in.middle(x)
			if len(m) != n {
				return false
			}
			for o := 0; o < n; o++ {
				if m[o] != in.target[o] {
					return false
				}
			}
			return true
		}
		g := groups[gi]
		var rec func(j int) bool
		rec = func(j int) bool {
			if j == len(g) {
				m := in.middle(x)
				if len(m) != n {
					return false
				}
				for _, o := range checks[gi] {
					if o >= len(m) || m[o] != in.target[o] {
						return false
					}
				}
				return dfs(gi + 1)
			}
			for _, sym := range in.alphabet {
				x[g[j]] = sym
				if rec(j + 1) {
					return true
				}
			}
			return false
		}
		return rec(0)
	}

	if dfs(0) {
		return string(x), true
	}
	return string(x), false
}

// probeControllers determines, for each output position, which input positions
// influence it. It varies each input across a few symbols (against the given
// base) and unions the affected outputs, so dependencies are not missed.
func (in *inverter) probeControllers(base []rune, n int) []map[int]bool {
	baseMid := in.middle(base)
	affects := make([]map[int]bool, n)
	// A handful of probe symbols spread across the alphabet.
	var probes []rune
	for _, idx := range []int{len(in.alphabet) - 1, len(in.alphabet) / 2, 1} {
		if idx >= 0 && idx < len(in.alphabet) {
			probes = append(probes, in.alphabet[idx])
		}
	}
	x := append([]rune(nil), base...)
	for p := 0; p < n; p++ {
		affects[p] = map[int]bool{p: true} // an input always controls its own slot
		orig := x[p]
		for _, sym := range probes {
			if sym == orig {
				continue
			}
			x[p] = sym
			m := in.middle(x)
			for o := 0; o < n && o < len(m); o++ {
				if m[o] != baseMid[o] {
					affects[p][o] = true
				}
			}
		}
		x[p] = orig
	}
	controllers := make([]map[int]bool, n)
	for o := range controllers {
		controllers[o] = map[int]bool{}
	}
	for p := 0; p < n; p++ {
		for o := range affects[p] {
			controllers[o][p] = true
		}
	}
	return controllers
}

// planOrder turns the controller sets into a solving plan: a sequence of input
// groups to brute-force, and for each group the output positions that become
// fully determined (and thus checkable) once the group is set. Greedy: always
// take the output needing the fewest still-unset inputs.
func planOrder(controllers []map[int]bool, n int) (groups [][]int, checks [][]int, ok bool) {
	setIn := make([]bool, n)
	determined := make([]bool, n)
	nSet := 0
	for nSet < n {
		// Pick the undetermined output with the smallest set of unset controllers.
		bestO, bestNeed := -1, maxGroup+1
		for o := 0; o < n; o++ {
			if determined[o] {
				continue
			}
			need := 0
			for p := range controllers[o] {
				if !setIn[p] {
					need++
				}
			}
			if need < bestNeed {
				bestNeed, bestO = need, o
			}
		}
		if bestO < 0 || bestNeed > maxGroup {
			return nil, nil, false
		}
		// The group = unset controllers of bestO.
		var grp []int
		for p := range controllers[bestO] {
			if !setIn[p] {
				grp = append(grp, p)
				setIn[p] = true
				nSet++
			}
		}
		if len(grp) == 0 {
			// bestO already determined by set inputs; just record its check.
			grp = nil
		}
		// Outputs now fully determined become this step's checks.
		var chk []int
		for o := 0; o < n; o++ {
			if determined[o] {
				continue
			}
			allSet := true
			for p := range controllers[o] {
				if !setIn[p] {
					allSet = false
					break
				}
			}
			if allSet {
				determined[o] = true
				chk = append(chk, o)
			}
		}
		groups = append(groups, grp)
		checks = append(checks, chk)
	}
	return groups, checks, true
}

// --- helpers ---------------------------------------------------------------

// expandReadable expands a glyph forward through Multiple/Single substitutions
// to its readable leaves (via cmap), producing the phrase it ultimately shows.
// A depth cap (not a visited-set) bounds recursion, so a glyph that legitimately
// recurs across branches (repeated letters) is not dropped.
func expandReadable(r *gsub.Rules, g gsub.GID, depth int) string {
	if depth > 256 {
		return ""
	}
	if seq, ok := r.Multiple[g]; ok {
		var b strings.Builder
		for _, x := range seq {
			b.WriteString(expandReadable(r, x, depth+1))
		}
		return b.String()
	}
	// A glyph that already has a readable character is a leaf - read it directly
	// rather than chasing a Single rule (the letter 'a' is itself an input to the
	// hexchar->g_aX substitution, which we must not follow here).
	if c, ok := r.Glyph2Char[g]; ok {
		return string(c)
	}
	if out, ok := r.Single[g]; ok && out != g {
		return expandReadable(r, out, depth+1)
	}
	return ""
}

func leadingReadable(r *gsub.Rules, comps []gsub.GID) (string, int) {
	var b strings.Builder
	n := 0
	for _, g := range comps {
		if c, ok := r.Glyph2Char[g]; ok {
			b.WriteRune(c)
			n++
		} else {
			break
		}
	}
	return b.String(), n
}

func trailingReadable(r *gsub.Rules, comps []gsub.GID, skipFront int) (string, int) {
	var rs []rune
	n := 0
	for i := len(comps) - 1; i >= skipFront; i-- {
		if c, ok := r.Glyph2Char[comps[i]]; ok {
			rs = append([]rune{c}, rs...)
			n++
		} else {
			break
		}
	}
	return string(rs), n
}

func readableScore(s string) int {
	n := 0
	for _, c := range s {
		if c == ' ' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			n++
		}
	}
	return n
}

func containsKeyword(s string) bool { return readableContainsAny(s, successKeywords) }

func readableContainsAny(s string, kws []string) bool {
	ls := strings.ToLower(s)
	for _, kw := range kws {
		if strings.Contains(ls, kw) {
			return true
		}
	}
	return false
}

func normalize(s string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(s), "."))
}

func quote(s string) string { return "\"" + s + "\"" }
