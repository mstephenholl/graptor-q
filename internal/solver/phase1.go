package solver

import "slices"

// The r = 2 components are rebuilt once more than 1/rebuildFactor of the
// bucket-2 rows are new since the last build. Measured at K' = 10017 and
// 50511: going from 8 to 2 makes plans 13% faster to build for 2% more
// inactivated columns; 1 gains little more but inactivates 5% more.
const rebuildFactor = 2

// Column states during and after phase 1.
const (
	colV        uint8 = iota // still in the submatrix V
	colPivot                 // chosen as the pivot of some row
	colInactive              // in U (permanently or by inactivation)
)

// phase1 is the symbolic first phase of inactivation decoding (RFC 6330
// Section 5.4.2.2) over the non-HDPC rows.
//
// Invariant: when a row is chosen with r nonzeros in V, one of those columns
// becomes its pivot and the other r-1 are inactivated. The row then has no
// other entries in V, so eliminating its pivot from the remaining rows only
// affects U. Consequently the V entries of unchosen rows are always their
// original entries, and the original pivot rows restricted to the pivot
// columns form a unit lower triangular matrix in pivot order.
type phase1 struct {
	rs       *rowSet
	colState []uint8  // per column
	colPiv   []int32  // pivot index per column, -1 if none
	pivRow   []int32  // pivot rows in pivot order
	pivCol   []uint16 // pivot columns in pivot order
	uCols    []uint16 // inactive columns: phase-1 inactivations, then the P PI columns
	uIdx     []int32  // U index per column, -1 if not in U
	vcols    []uint16 // scratch

	// Lazy bucket queue of unchosen rows by their number of nonzeros in V
	// (vdeg, which is chosenRow for the rows chosen as pivots): bucket[d] is
	// a stack of rows that had degree d when pushed. A row is pushed again
	// when its degree drops, so an entry is stale (and skipped) if the row
	// has been chosen or its degree has changed since. Within a bucket the
	// valid entries, from the top, are the rows in the order they entered it,
	// most recent first.
	vdeg   []int32
	bucket [][]int32
	minR   int

	// union-find scratch for the r = 2 rule
	ufParent, ufSize []int32
	ufStamp          []uint32
	ufGen            uint32
	pairs            []int32

	// Components of the last graph built for the r = 2 rule: the rows
	// (edges) grouped by component, largest component first. They are
	// reused until enough rows have entered bucket 2 to make them stale.
	compRows  []int32
	compStart []int32
	compFill  []int32
	comps     [][2]int32 // (root, size)
	ufComp    []int32
	compNext  int // current component
	compPos   int // next row to try within compRows
	builtTwos int // bucket-2 size when the components were built
	newTwos   int // rows that entered bucket 2 since then
	haveComp  bool
}

func runPhase1(rs *rowSet) *phase1 {
	ph := new(phase1)
	ph.run(rs)
	return ph
}

// run performs phase 1 on rs, reusing the memory of ph.
func (ph *phase1) run(rs *rowSet) {
	p := rs.p
	W, L := p.W, p.L
	n := rs.nrows()
	ph.rs = rs
	ph.colState = zeroed(ph.colState, L)
	ph.colPiv = filled(ph.colPiv, L, -1)
	ph.vdeg = resize(ph.vdeg, n)
	ph.pivRow = ph.pivRow[:0]
	ph.pivCol = ph.pivCol[:0]
	ph.uCols = ph.uCols[:0]
	ph.haveComp = false
	ph.compNext, ph.compPos, ph.builtTwos, ph.newTwos = 0, 0, 0, 0
	for c := W; c < L; c++ {
		ph.colState[c] = colInactive
	}

	maxDeg := int32(0)
	for r := range n {
		d := int32(0)
		for _, c := range rs.row(r) {
			if int(c) < W {
				d++
			}
		}
		ph.vdeg[r] = d
		maxDeg = max(maxDeg, d)
	}
	ph.bucket = resize(ph.bucket, int(maxDeg)+1)
	for d := range ph.bucket {
		ph.bucket[d] = ph.bucket[d][:0]
	}
	// Push in reverse so that each bucket lists rows in increasing order.
	for r := n - 1; r >= 0; r-- {
		if ph.vdeg[r] > 0 {
			ph.push(int32(r))
		}
	}
	ph.minR = 1

	remaining := W
	for remaining > 0 {
		for ph.minR < len(ph.bucket) && ph.top(ph.minR) < 0 {
			ph.minR++
		}
		if ph.minR >= len(ph.bucket) {
			// No unchosen row has an entry in V. The remaining V columns
			// can only be resolved by HDPC rows in phase 2 (or not at all).
			for c := range W {
				if ph.colState[c] == colV {
					ph.colState[c] = colInactive
					ph.uCols = append(ph.uCols, uint16(c))
				}
			}
			break
		}

		var row int32
		switch r := ph.minR; r {
		case 1:
			row = ph.top(1)
		case 2:
			row = ph.componentRow()
		default:
			row = ph.minDegreeRow(r)
		}

		vcols := ph.vcols[:0]
		for _, c := range rs.row(int(row)) {
			if int(c) < W && ph.colState[c] == colV {
				vcols = append(vcols, c)
			}
		}
		ph.vcols = vcols
		// The pivot is the column appearing in the fewest rows; the others
		// are inactivated, which lowers r for as many rows as possible.
		best := 0
		for i := 1; i < len(vcols); i++ {
			if rs.cstart[vcols[i]+1]-rs.cstart[vcols[i]] < rs.cstart[vcols[best]+1]-rs.cstart[vcols[best]] {
				best = i
			}
		}

		ph.vdeg[row] = chosenRow // its bucket entry is now stale
		piv := vcols[best]
		ph.colState[piv] = colPivot
		ph.colPiv[piv] = int32(len(ph.pivRow))
		ph.pivRow = append(ph.pivRow, row)
		ph.pivCol = append(ph.pivCol, piv)
		ph.leaveV(piv)
		for i, c := range vcols {
			if i != best {
				ph.colState[c] = colInactive
				ph.uCols = append(ph.uCols, c)
				ph.leaveV(c)
			}
		}
		remaining -= len(vcols)
	}

	// U: the columns inactivated above, then the P PI columns.
	for c := W; c < L; c++ {
		ph.uCols = append(ph.uCols, uint16(c))
	}
	ph.uIdx = filled(ph.uIdx, L, -1)
	for i, c := range ph.uCols {
		ph.uIdx[c] = int32(i)
	}
}

// chosenRow is the V degree of a row chosen as a pivot. Keeping it in vdeg
// rather than in a separate array saves a cache miss per row visited.
const chosenRow = -1

// leaveV updates the V degrees of the unchosen rows containing column c,
// which has just left V (as a pivot or by inactivation).
func (ph *phase1) leaveV(c uint16) {
	for _, r := range ph.rs.colRows(int(c)) {
		if ph.vdeg[r] == chosenRow {
			continue
		}
		ph.vdeg[r]--
		if d := ph.vdeg[r]; d > 0 {
			ph.push(r) // the entry in bucket d+1 is now stale
			if int(d) < ph.minR {
				ph.minR = int(d)
			}
		}
	}
}

func (ph *phase1) push(r int32) {
	d := ph.vdeg[r]
	if d == 2 {
		ph.newTwos++
	}
	ph.bucket[d] = append(ph.bucket[d], r)
}

// valid reports whether a bucket-d entry for row r is current (d >= 1, so
// entries of chosen rows are not).
func (ph *phase1) valid(r int32, d int) bool {
	return int(ph.vdeg[r]) == d
}

// top returns the most recent valid row of bucket d, dropping stale entries
// above it, or -1 if there is none.
func (ph *phase1) top(d int) int32 {
	b := ph.bucket[d]
	for len(b) > 0 {
		if r := b[len(b)-1]; ph.valid(r, d) {
			ph.bucket[d] = b
			return r
		}
		b = b[:len(b)-1]
	}
	ph.bucket[d] = b
	return -1
}

// minDegreeRow returns a row with r nonzeros in V of minimum original degree
// (the most recent such row on ties).
func (ph *phase1) minDegreeRow(r int) int32 {
	best, bestDeg := int32(-1), int32(0)
	b := ph.bucket[r]
	for _, x := range slices.Backward(b) {
		if !ph.valid(x, r) {
			continue
		}
		if d := ph.rs.start[x+1] - ph.rs.start[x]; best < 0 || d < bestDeg {
			best, bestDeg = x, d
		}
	}
	return best
}

// componentRow implements the r = 2 rule of Section 5.4.2.2: in the graph
// whose nodes are the columns of V and whose edges are the rows with exactly
// two ones in V, choose a row that is part of a maximum size component.
//
// Rebuilding the graph for every r = 2 step is quadratic for large K', so the
// components of the last build are reused (largest first) while few new
// edges have appeared since. The rule only affects how many columns are
// inactivated, never the correctness of the solution.
func (ph *phase1) componentRow() int32 {
	if !ph.haveComp || ph.newTwos*rebuildFactor > ph.builtTwos {
		ph.buildComponents()
	}
	if r := ph.nextComponentRow(); r >= 0 {
		return r
	}
	ph.buildComponents()
	return ph.nextComponentRow()
}

// nextComponentRow returns the next row of the stored components that still
// has exactly two nonzeros in V, or -1 if none is left. Such a row is the
// same edge as when the components were built, since V degrees only drop.
func (ph *phase1) nextComponentRow() int32 {
	for ph.compNext < len(ph.compStart)-1 {
		for ph.compPos < int(ph.compStart[ph.compNext+1]) {
			r := ph.compRows[ph.compPos]
			if ph.vdeg[r] == 2 {
				return r
			}
			ph.compPos++
		}
		ph.compNext++
	}
	return -1
}

func (ph *phase1) buildComponents() {
	W := ph.rs.p.W
	if len(ph.ufParent) < W { // a reused phase1 may have served a smaller K'
		ph.ufParent = make([]int32, W)
		ph.ufSize = make([]int32, W)
		ph.ufStamp = make([]uint32, W)
		ph.ufComp = make([]int32, W)
	}
	ph.ufGen++
	if ph.ufGen == 0 { // wrapped around: stale stamps could match
		clear(ph.ufStamp)
		ph.ufGen = 1
	}
	ph.pairs = ph.pairs[:0]
	b := ph.bucket[2]
	for _, r := range slices.Backward(b) {
		if !ph.valid(r, 2) {
			continue
		}
		var ends [2]int32
		k := 0
		for _, c := range ph.rs.row(int(r)) {
			if int(c) < W && ph.colState[c] == colV {
				ends[k] = int32(c)
				k++
				if k == 2 {
					break
				}
			}
		}
		ph.pairs = append(ph.pairs, r, ends[0])
		a, b := ph.find(ends[0]), ph.find(ends[1])
		if a != b {
			if ph.ufSize[a] < ph.ufSize[b] {
				a, b = b, a
			}
			ph.ufParent[b] = a
			ph.ufSize[a] += ph.ufSize[b]
		}
	}
	// Group the rows by component (counting sort on the root), then order
	// the components by decreasing size.
	// ufComp[root] is the component index of a root; a root is new to this
	// build when its ufSize is still positive (it is negated once indexed).
	comps := ph.comps[:0]
	for i := 0; i < len(ph.pairs); i += 2 {
		root := ph.find(ph.pairs[i+1])
		ph.pairs[i+1] = root
		if s := ph.ufSize[root]; s > 0 {
			ph.ufComp[root] = int32(len(comps))
			comps = append(comps, [2]int32{root, s})
			ph.ufSize[root] = -s
		}
	}
	ph.comps = comps
	slices.SortStableFunc(comps, func(a, b [2]int32) int { return int(b[1] - a[1]) })
	for i, c := range comps {
		ph.ufComp[c[0]] = int32(i)
	}
	counts := slices.Grow(ph.compStart[:0], len(comps)+1)[:len(comps)+1]
	clear(counts)
	for i := 0; i < len(ph.pairs); i += 2 {
		counts[ph.ufComp[ph.pairs[i+1]]+1]++
	}
	for i := range comps {
		counts[i+1] += counts[i]
	}
	ph.compStart = counts
	ph.compRows = slices.Grow(ph.compRows[:0], len(ph.pairs)/2)[:len(ph.pairs)/2]
	fill := slices.Grow(ph.compFill[:0], len(comps))[:len(comps)]
	copy(fill, counts)
	ph.compFill = fill
	for i := 0; i < len(ph.pairs); i += 2 {
		c := ph.ufComp[ph.pairs[i+1]]
		ph.compRows[fill[c]] = ph.pairs[i]
		fill[c]++
	}
	ph.compNext, ph.compPos = 0, 0
	ph.builtTwos = len(ph.pairs) / 2
	ph.newTwos = 0
	ph.haveComp = true
}

func (ph *phase1) find(c int32) int32 {
	if ph.ufStamp[c] != ph.ufGen {
		ph.ufStamp[c] = ph.ufGen
		ph.ufParent[c] = c
		ph.ufSize[c] = 1
		return c
	}
	for ph.ufParent[c] != c {
		ph.ufParent[c] = ph.ufParent[ph.ufParent[c]]
		c = ph.ufParent[c]
	}
	return c
}
