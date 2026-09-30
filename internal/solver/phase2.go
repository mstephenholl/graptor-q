package solver

import (
	"math/bits"
	"sync"

	"github.com/mholland/graptorq/internal/gf256"
	"github.com/mholland/graptorq/internal/rfc"
)

// Phase-2 row operations on right-hand sides, recorded for the plan.
const (
	p2Xor    uint8 = iota // rhs[dst] ^= rhs[src]
	p2MulAdd              // rhs[dst] ^= c * rhs[src]
	p2Scale               // rhs[dst] = c * rhs[dst]
)

type p2op struct {
	kind     uint8
	c        byte
	dst, src int32 // phase-2 row ids
}

// phase2 is the solution of the reduced system over the u columns of U.
//
// Its rows are the nb unchosen non-HDPC rows (binary, ids 0..nb-1) followed
// by the H HDPC rows (ids nb..nb+H-1). Row t of the reduced system is
//
//	(X_t[U] + sum of A'_q over pivots q in row t) * C_U = D_t + sum of y_q
//
// where y_q and A'_q are the right-hand side and U part of pivot row q after
// forward substitution through the earlier pivots (see computeUParts).
type phase2 struct {
	cand   []int32 // non-HDPC row of binary phase-2 row i
	nb     int
	colRow []int32 // per U column: the phase-2 row holding its value at the end
	rowCol []int32 // per phase-2 row: its U column, or -1 if the row is not used
	ops    []p2op  // live operations only
}

func runPhase2(ph *phase1) (*phase2, error) {
	words, _, cand, candBits, hd := computeUParts(ph)
	u := len(ph.uCols)
	nb := len(cand)
	H := len(hd)

	p2 := &phase2{cand: cand, nb: nb, colRow: make([]int32, u), rowCol: make([]int32, nb+H)}
	for i := range p2.rowCol {
		p2.rowCol[i] = -1
	}
	var ops []p2op

	// Forward GF(2) elimination over the binary rows. A column without a
	// binary pivot is deferred to the HDPC rows. An unprocessed row never
	// has a bit in an earlier column: pivot columns were eliminated from it
	// and deferred columns had no unprocessed row with that bit.
	unproc := make([]int32, nb)
	for i := range unproc {
		unproc[i] = int32(i)
	}
	var order, deferred []int32
	for c := range u {
		w, bit := c>>6, uint64(1)<<(c&63)
		pi := -1
		for k, r := range unproc {
			if candBits[int(r)*words+w]&bit != 0 {
				pi = k
				break
			}
		}
		if pi < 0 {
			deferred = append(deferred, int32(c))
			continue
		}
		r := unproc[pi]
		copy(unproc[pi:], unproc[pi+1:])
		unproc = unproc[:len(unproc)-1]
		rb := candBits[int(r)*words : int(r+1)*words]
		for _, t := range unproc {
			tb := candBits[int(t)*words : int(t+1)*words]
			if tb[w]&bit != 0 {
				for k := w; k < words; k++ {
					tb[k] ^= rb[k]
				}
				ops = append(ops, p2op{kind: p2Xor, dst: t, src: r})
			}
		}
		for h := range H {
			if beta := hd[h][c]; beta != 0 {
				row := hd[h]
				for k := w; k < words; k++ {
					for x := rb[k]; x != 0; x &= x - 1 {
						row[k<<6+bits.TrailingZeros64(x)] ^= beta
					}
				}
				ops = append(ops, p2op{kind: p2MulAdd, c: beta, dst: int32(nb + h), src: r})
			}
		}
		p2.colRow[c] = r
		p2.rowCol[r] = int32(c)
		order = append(order, r)
	}

	// Gauss-Jordan over the HDPC rows, which now only have entries in the
	// deferred columns.
	for _, c := range deferred {
		hp := -1
		for h := range H {
			if p2.rowCol[nb+h] < 0 && hd[h][c] != 0 {
				hp = h
				break
			}
		}
		if hp < 0 {
			return nil, ErrSingular
		}
		if beta := hd[hp][c]; beta != 1 {
			inv := gf256.Inv(beta)
			gf256.MulSlice(hd[hp], hd[hp], inv)
			ops = append(ops, p2op{kind: p2Scale, c: inv, dst: int32(nb + hp)})
		}
		for h := range H {
			if g := hd[h][c]; h != hp && g != 0 {
				gf256.MulAddSlice(hd[h], hd[hp], g)
				ops = append(ops, p2op{kind: p2MulAdd, c: g, dst: int32(nb + h), src: int32(nb + hp)})
			}
		}
		p2.colRow[c] = int32(nb + hp)
		p2.rowCol[nb+hp] = c
	}

	// Back substitution through the binary pivot rows, last pivot first.
	for k := len(order) - 1; k >= 0; k-- {
		r := order[k]
		c := int(p2.rowCol[r])
		rb := candBits[int(r)*words : int(r+1)*words]
		for i, x := range rb {
			for ; x != 0; x &= x - 1 {
				if j := i<<6 + bits.TrailingZeros64(x); j != c {
					ops = append(ops, p2op{kind: p2Xor, dst: r, src: p2.colRow[j]})
				}
			}
		}
	}

	// Dead-code elimination: operations on rows that are never used as a
	// pivot (redundant equations) do not affect the solution.
	live := ops[:0]
	for _, op := range ops {
		if p2.rowCol[op.dst] >= 0 {
			live = append(live, op)
		}
	}
	p2.ops = live
	return p2, nil
}

// computeUParts forward-substitutes the phase-1 pivot rows through each
// other and expresses every remaining row in terms of U only.
//
// It returns the U parts A'_p of the pivot rows (as bitsets of the given
// number of 64-bit words each), the unchosen non-HDPC rows with their reduced
// U parts, and the H reduced HDPC rows as dense GF(256) rows of length u.
func computeUParts(ph *phase1) (words int, pivBits []uint64, cand []int32, candBits []uint64, hd [][]byte) {
	rs := ph.rs
	u := len(ph.uCols)
	words = (u + 63) / 64
	pivBits = make([]uint64, len(ph.pivRow)*words)
	for i, r := range ph.pivRow {
		dst := pivBits[i*words : (i+1)*words]
		for _, c := range rs.row(int(r)) {
			if q := int(ph.colPiv[c]); q >= 0 {
				if q > i {
					panic("solver: pivot rows are not lower triangular")
				}
				if q < i {
					xorWords(dst, pivBits[q*words:(q+1)*words])
				}
			} else {
				k := ph.uIdx[c]
				dst[k>>6] ^= 1 << (k & 63)
			}
		}
	}

	for r := range rs.nrows() {
		if !ph.chosen[r] {
			cand = append(cand, int32(r))
		}
	}
	candBits = make([]uint64, len(cand)*words)
	for i, r := range cand {
		dst := candBits[i*words : (i+1)*words]
		for _, c := range rs.row(int(r)) {
			if q := int(ph.colPiv[c]); q >= 0 {
				xorWords(dst, pivBits[q*words:(q+1)*words])
			} else {
				k := ph.uIdx[c]
				dst[k>>6] ^= 1 << (k & 63)
			}
		}
	}

	hd = hdpcRows(ph, pivBits, words)
	return words, pivBits, cand, candBits, hd
}

// hdpcRows computes the reduced HDPC rows G_HDPC * X + I_H, where row j of X
// is A'_q if column j is the pivot of q, or the unit vector of column j if it
// is in U. G_HDPC = MT * GAMMA is applied with the recurrence
// z_j = alpha*z_{j-1} + X_j (GAMMA) followed by scattering z_j into the rows
// of MT that have a nonzero in column j.
func hdpcRows(ph *phase1, pivBits []uint64, words int) [][]byte {
	p := ph.rs.p
	u := len(ph.uCols)
	H, n := p.H, p.KPrime+p.S
	pairs := hdpcPairs(p)
	buf := make([]byte, (H+1)*u)
	hd := make([][]byte, H)
	for h := range hd {
		hd[h] = buf[h*u : (h+1)*u : (h+1)*u]
	}
	z := buf[H*u:]
	for j := range n {
		if j > 0 {
			gf256.MulSlice(z, z, 2)
		}
		if q := int(ph.colPiv[j]); q >= 0 {
			for i, x := range pivBits[q*words : (q+1)*words] {
				for ; x != 0; x &= x - 1 {
					z[i<<6+bits.TrailingZeros64(x)] ^= 1
				}
			}
		} else {
			z[ph.uIdx[j]] ^= 1
		}
		if j < n-1 {
			gf256.AddSlice(hd[pairs.r1[j]], z)
			gf256.AddSlice(hd[pairs.r2[j]], z)
		} else {
			for h := range H {
				gf256.MulAddSlice(hd[h], z, gf256.Exp(h))
			}
		}
	}
	for h := range H {
		hd[h][ph.uIdx[n+h]] ^= 1
	}
	return hd
}

func xorWords(dst, src []uint64) {
	src = src[:len(dst)]
	for i := range dst {
		dst[i] ^= src[i]
	}
}

type mtPairs struct{ r1, r2 []uint8 }

var hdpcPairCache sync.Map // K' -> *mtPairs

func hdpcPairs(p *rfc.Params) *mtPairs {
	if v, ok := hdpcPairCache.Load(p.KPrime); ok {
		return v.(*mtPairs)
	}
	r1, r2 := p.HDPCPairs()
	v, _ := hdpcPairCache.LoadOrStore(p.KPrime, &mtPairs{r1, r2})
	return v.(*mtPairs)
}
