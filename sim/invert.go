package sim

import "strings"

// This file inverts a GSUB cipher that is built as a sequence of rounds, each
// mapping a 44-wide vector of "column" glyphs to the next column. The full
// cipher is full-diffusion, but every individual round mixes only neighbouring
// positions, so each round is inverted by a cheap local search and the inverses
// are chained. This recovers the typed secret from the target glyph sequence.

// hexDigits is the lowercase hex used for the recovered flag text. colHex is the
// UPPERCASE hex used in glyph names (the font names column glyphs g_aA..g_aF for
// nibble values 10..15).
const hexDigits = "0123456789abcdef"
const colHex = "0123456789ABCDEF"

// checkpoint is a clean state: the buffer is the frame plus 44 glyphs that are
// all of one g_<fam> column.
type checkpoint struct {
	orderIdx  int   // last feature-order index producing this clean state
	fam       byte  // column family letter
	buf       []GID // full buffer at this checkpoint
	positions []int // indices of the 44 column glyphs in buf
	base      GID   // gid of g_<fam>0 (column glyphs are base..base+15)
}

// Invert recovers the secret from the target column glyphs (the glyphs the
// recognition rule expects, frame stripped). prefix/suffix are the literal frame
// around the secret (e.g. "PVIB{" and "}"); the secret width and the per-column
// alphabet size are derived from the font, not hard-coded.
func (e *Engine) Invert(prefix, suffix string, target []GID) (string, bool) {
	idx := e.nameIndex()
	width := len(target)
	cks := e.checkpoints(idx, prefix, suffix, width)
	if len(cks) < 2 {
		return "", false
	}
	// The last checkpoint is the cipher output the target lives in.
	last := cks[len(cks)-1]
	if len(target) != len(last.positions) {
		return "", false
	}

	// Current full buffer = last checkpoint's template with target glyphs placed.
	cur := append([]GID(nil), last.buf...)
	for i, p := range last.positions {
		cur[p] = target[i]
	}

	// Invert rounds from last down to first.
	for r := len(cks) - 1; r >= 1; r-- {
		prev := cks[r-1]
		lo, hi := prev.orderIdx+1, cks[r].orderIdx+1
		col := columnGlyphs(idx, prev.fam)
		if len(col) == 0 {
			return "", false
		}
		// forward maps a nibble vector (placed on prev's template) through this
		// round, returning the resulting column glyphs at cks[r]'s positions.
		forward := func(nib []int) []GID {
			b := append([]GID(nil), prev.buf...)
			for i, p := range prev.positions {
				b[p] = col[nib[i]]
			}
			out := e.ForwardRange(b, lo, hi)
			res := make([]GID, len(cks[r].positions))
			for i, p := range cks[r].positions {
				if p < len(out) {
					res[i] = out[p]
				}
			}
			return res
		}
		want := make([]GID, len(cks[r].positions))
		for i, p := range cks[r].positions {
			want[i] = cur[p]
		}
		nib, ok := solveRound(forward, want, len(col))
		if !ok {
			return "", false
		}
		// Rebuild current buffer at prev checkpoint.
		b := append([]GID(nil), prev.buf...)
		for i, p := range prev.positions {
			b[p] = col[nib[i]]
		}
		cur = b
	}

	// cur is now the first column (checkpoint[0]); each glyph's index is the hex.
	first := cks[0]
	colA := columnGlyphs(idx, first.fam)
	if len(colA) == 0 {
		return "", false
	}
	rev := map[GID]int{}
	for k, g := range colA {
		rev[g] = k
	}
	var sb strings.Builder
	for _, p := range first.positions {
		n, ok := rev[cur[p]]
		if !ok {
			return "", false
		}
		sb.WriteByte(hexDigits[n])
	}
	return sb.String(), true
}

// checkpoints runs a reference input forward and records every clean column
// state, with its frame template and the column positions.
func (e *Engine) checkpoints(idx map[string]GID, prefix, suffix string, width int) []checkpoint {
	start := e.Glyphs(prefix + strings.Repeat("0", width) + suffix)
	var cks []checkpoint
	for k := 0; k < e.NumOrder(); k++ {
		buf := e.ForwardRange(start, 0, k+1)
		fam, pos, ok := cleanState(e, buf, width)
		if !ok {
			continue
		}
		if len(cks) > 0 && cks[len(cks)-1].fam == fam {
			// Same family run: keep the FIRST clean state. Extending to the last
			// would pull trailing no-op lookups into the next round's range - and
			// for the final column those include the recognition collapse, which
			// fires for the correct candidate and hides the target glyphs.
			continue
		}
		base := GID(0)
		if len(pos) > 0 {
			g := buf[pos[0]]
			n := e.Name(g)
			base = g - GID(hexVal(n[3]))
		}
		cks = append(cks, checkpoint{orderIdx: k, fam: fam, buf: append([]GID(nil), buf...), positions: pos, base: base})
	}
	return cks
}

// cleanState reports whether buf is frame + width single-column glyphs,
// returning the column family and the positions of those glyphs.
func cleanState(e *Engine, buf []GID, width int) (byte, []int, bool) {
	fam := byte(0)
	var pos []int
	for i, g := range buf {
		n := e.Name(g)
		if strings.HasPrefix(n, "g_") && len(n) == 4 && isHexByte(n[3]) {
			c := n[2]
			if fam == 0 {
				fam = c
			} else if fam != c {
				return 0, nil, false
			}
			pos = append(pos, i)
		}
	}
	if fam == 0 || len(pos) != width {
		return 0, nil, false
	}
	return fam, pos, true
}

// nameIndex builds a glyph-name -> gid map (cached) for column lookups.
func (e *Engine) nameIndex() map[string]GID {
	if e.names != nil {
		return e.names
	}
	m := map[string]GID{}
	empties := 0
	for g := GID(0); g < 1<<16; g++ {
		n := e.face.Font.GlyphName(g)
		if n == "" {
			empties++
			if empties > 200 && g > 256 {
				break
			}
			continue
		}
		empties = 0
		m[n] = g
	}
	e.names = m
	return m
}

// columnGlyphs returns the column's glyph variants ordered by hex value
// (g_<fam>0, g_<fam>1, ..., g_<fam>F), deriving the alphabet size from the
// variants that actually exist rather than assuming 16.
func columnGlyphs(idx map[string]GID, fam byte) []GID {
	var col []GID
	for k := 0; k < 16; k++ {
		g, ok := idx["g_"+string(fam)+string(colHex[k])]
		if !ok {
			break
		}
		col = append(col, g)
	}
	return col
}

func isHexByte(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func hexVal(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	}
	return 0
}

// solveRound inverts one round: find a nibble vector whose forward image equals
// target. Round diffusion is local, so we probe per-output dependencies and
// solve in small groups with backtracking.
func solveRound(forward func([]int) []GID, target []GID, alphabet int) ([]int, bool) {
	n := len(target)
	x := make([]int, n)
	if !eqG(forwardLen(forward, x), target) && len(forward(x)) != n {
		return nil, false
	}

	// Probe controllers: vary each input across a couple values, union changed outputs.
	base := forward(x)
	if len(base) != n {
		return nil, false
	}
	controllers := make([]map[int]bool, n)
	for o := range controllers {
		controllers[o] = map[int]bool{}
	}
	for p := 0; p < n; p++ {
		for _, v := range []int{alphabet - 1, alphabet / 2, 1} {
			if v == x[p] {
				continue
			}
			orig := x[p]
			x[p] = v
			m := forward(x)
			x[p] = orig
			for o := 0; o < n && o < len(m); o++ {
				if m[o] != base[o] {
					controllers[o][p] = true
				}
			}
		}
		controllers[p][p] = true
	}

	groups, checks, ok := planGroups(controllers, n, 3)
	if !ok {
		return nil, false
	}

	var dfs func(gi int) bool
	dfs = func(gi int) bool {
		if gi == len(groups) {
			return eqG(forward(x), target)
		}
		g := groups[gi]
		var rec func(j int) bool
		rec = func(j int) bool {
			if j == len(g) {
				m := forward(x)
				for _, o := range checks[gi] {
					if o >= len(m) || m[o] != target[o] {
						return false
					}
				}
				return dfs(gi + 1)
			}
			for v := 0; v < alphabet; v++ {
				x[g[j]] = v
				if rec(j + 1) {
					return true
				}
			}
			return false
		}
		return rec(0)
	}
	if dfs(0) {
		return append([]int(nil), x...), true
	}
	return nil, false
}

func forwardLen(forward func([]int) []GID, x []int) []GID { return forward(x) }

func eqG(a, b []GID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// planGroups orders inputs into solvable groups (smallest unset-controller set
// first), with the outputs each group determines.
func planGroups(controllers []map[int]bool, n, maxGroup int) (groups [][]int, checks [][]int, ok bool) {
	setIn := make([]bool, n)
	determined := make([]bool, n)
	nSet := 0
	for nSet < n {
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
		var grp []int
		for p := range controllers[bestO] {
			if !setIn[p] {
				grp = append(grp, p)
				setIn[p] = true
				nSet++
			}
		}
		var chk []int
		for o := 0; o < n; o++ {
			if determined[o] {
				continue
			}
			all := true
			for p := range controllers[o] {
				if !setIn[p] {
					all = false
					break
				}
			}
			if all {
				determined[o] = true
				chk = append(chk, o)
			}
		}
		groups = append(groups, grp)
		checks = append(checks, chk)
	}
	return groups, checks, true
}
