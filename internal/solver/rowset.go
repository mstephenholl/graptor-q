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
}

func newRowSet(p *rfc.Params, isis []uint32) *rowSet {
	n := p.S + len(isis)
	rs := &rowSet{p: p, start: make([]int32, 1, n+1)}
	rs.cols = make([]uint16, 0, 3*p.B+3*p.S+len(isis)*8)
	for _, r := range p.LDPCRows() {
		rs.cols = append(rs.cols, r...)
		rs.start = append(rs.start, int32(len(rs.cols)))
	}
	for _, x := range isis {
		n := len(rs.cols)
		rs.cols = p.AppendEncCols(rs.cols, x)
		rs.cols = append(rs.cols[:n], rfc.CancelPairs(rs.cols[n:])...)
		rs.start = append(rs.start, int32(len(rs.cols)))
	}

	W := p.W
	rs.cstart = make([]int32, W+1)
	for _, c := range rs.cols {
		if int(c) < W {
			rs.cstart[c+1]++
		}
	}
	for c := range W {
		rs.cstart[c+1] += rs.cstart[c]
	}
	rs.crows = make([]int32, rs.cstart[W])
	fill := make([]int32, W)
	copy(fill, rs.cstart[:W])
	for r := range rs.nrows() {
		for _, c := range rs.row(r) {
			if int(c) < W {
				rs.crows[fill[c]] = int32(r)
				fill[c]++
			}
		}
	}
	return rs
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
