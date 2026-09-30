// Package solver computes the intermediate symbols of RFC 6330 by
// inactivation decoding.
//
// The algorithm follows the structure of RFC 6330 Section 5.4.2, with the
// observation that its first phase only depends on the sparsity pattern of the
// constraint matrix: a pivot row keeps only its pivot column in V (the other
// columns are inactivated), so V never fills in, and eliminating a pivot from
// a row uses that row's original coefficient. Solving is therefore split into
// a symbolic part, which produces a Plan (a straight-line program of symbol
// operations), and the numeric execution of that Plan over the symbol data.
//
// Because the Plan does not depend on the symbol size or contents, the
// encoder caches one Plan per K' (its constraint matrix is fixed).
package solver

import (
	"errors"
	"sync"

	"github.com/mholland/graptorq/internal/rfc"
)

// ErrSingular reports that the constraint matrix does not have full rank:
// more encoding symbols are needed to decode.
var ErrSingular = errors.New("solver: constraint matrix is rank deficient")

// rowSet holds the sparsity pattern of the non-HDPC constraint rows: the S
// LDPC rows followed by one LT row per ISI. HDPC rows are never materialized.
type rowSet struct {
	p     *rfc.Params
	start []int32 // CSR row offsets, len nrows+1
	cols  []uint16
	// CSC view of the LT columns (< W): rows containing each column.
	cstart []int32 // len W+1
	crows  []int32
	fill   []int32 // scratch
}

func newRowSet(p *rfc.Params, isis []uint32) *rowSet {
	rs := new(rowSet)
	rs.build(p, isis)
	return rs
}

// build fills rs for the given rows, reusing its memory.
func (rs *rowSet) build(p *rfc.Params, isis []uint32) {
	rs.p = p
	ldpc := ldpcRows(p)
	rs.start = append(rs.start[:0], ldpc.start...)
	rs.cols = append(rs.cols[:0], ldpc.cols...)
	base := baseRows(p)
	for _, x := range isis {
		// LT row columns are always distinct, so no cancellation is needed.
		if int(x) < p.KPrime {
			rs.cols = append(rs.cols, base.cols[base.start[x]:base.start[x+1]]...)
		} else {
			rs.cols = p.AppendEncCols(rs.cols, x)
		}
		rs.start = append(rs.start, int32(len(rs.cols)))
	}

	W := p.W
	rs.cstart = zeroed(rs.cstart, W+1)
	for _, c := range rs.cols {
		if int(c) < W {
			rs.cstart[c+1]++
		}
	}
	for c := range W {
		rs.cstart[c+1] += rs.cstart[c]
	}
	rs.crows = resize(rs.crows, int(rs.cstart[W]))
	rs.fill = resize(rs.fill, W)
	copy(rs.fill, rs.cstart[:W])
	for r := range rs.nrows() {
		for _, c := range rs.row(r) {
			if int(c) < W {
				rs.crows[rs.fill[c]] = int32(r)
				rs.fill[c]++
			}
		}
	}
}

// csr is a list of rows of column indices.
type csr struct {
	start []int32 // len rows+1, start[0] = 0
	cols  []uint16
}

var ldpcCache syncCache[*csr]

// ldpcRows returns the S LDPC rows of p, computed once per K'.
func ldpcRows(p *rfc.Params) *csr {
	if c, ok := ldpcCache.load(p.KPrime); ok {
		return c
	}
	c := &csr{start: []int32{0}}
	for _, r := range p.LDPCRows() {
		c.cols = append(c.cols, r...)
		c.start = append(c.start, int32(len(c.cols)))
	}
	return ldpcCache.loadOrStore(p.KPrime, c)
}

// baseCache keeps the LT rows of ISIs 0..K'-1 for a few recent K': a
// decoder's source and padding symbols are all among them, so their rows
// are copied instead of recomputed. An entry takes about 18 bytes per
// symbol (1 MB for K' = 56403).
var baseCache struct {
	mu      sync.Mutex
	entries [4]*baseEntry
	next    int
}

type baseEntry struct {
	kPrime int
	rows   *csr
}

// baseRows returns the LT rows of ISIs 0..K'-1 of p.
func baseRows(p *rfc.Params) *csr {
	baseCache.mu.Lock()
	for _, e := range baseCache.entries {
		if e != nil && e.kPrime == p.KPrime {
			baseCache.mu.Unlock()
			return e.rows
		}
	}
	baseCache.mu.Unlock()

	c := &csr{start: make([]int32, 1, p.KPrime+1)}
	c.cols = make([]uint16, 0, p.KPrime*8)
	for x := range uint32(p.KPrime) {
		c.cols = p.AppendEncCols(c.cols, x)
		c.start = append(c.start, int32(len(c.cols)))
	}
	baseCache.mu.Lock()
	baseCache.entries[baseCache.next] = &baseEntry{p.KPrime, c}
	baseCache.next = (baseCache.next + 1) % len(baseCache.entries)
	baseCache.mu.Unlock()
	return c
}

func (rs *rowSet) nrows() int            { return len(rs.start) - 1 }
func (rs *rowSet) row(r int) []uint16    { return rs.cols[rs.start[r]:rs.start[r+1]] }
func (rs *rowSet) colRows(c int) []int32 { return rs.crows[rs.cstart[c]:rs.cstart[c+1]] }

// input returns the index of the input symbol forming the right-hand side of
// non-HDPC row r, or -1 for LDPC rows (whose right-hand side is zero).
func (rs *rowSet) input(r int) int32 {
	if r < rs.p.S {
		return -1
	}
	return int32(r - rs.p.S)
}
