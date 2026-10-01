package solver

import (
	"math/bits"
	"slices"

	"github.com/mholland/graptorq/internal/gf256"
	"github.com/mholland/graptorq/internal/rfc"
)

// Phase-2 row operations on right-hand sides, recorded for the plan.
const (
	p2XorN   uint8 = iota // rhs[dst] ^= rhs[args[a0]] ^ rhs[args[a0+1]] ^ ... ^ rhs[args[a1-1]]
	p2MulAdd              // rhs[dst] ^= c * rhs[src]
	p2Scale               // rhs[dst] = c * rhs[dst]
)

type p2op struct {
	kind     uint8
	c        byte
	dst, src int32 // phase-2 row ids
	a0, a1   int32 // p2XorN: source rows p2.args[a0:a1]
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
	args   []int32 // sources of p2XorN operations

	// scratch
	words              int      // 64-bit words per U bitset
	pivBits, candBits  []uint64 // U parts of the pivot and candidate rows
	hd                 [][]byte // reduced HDPC rows
	hdBytes            []byte
	unitBits           []uint64 // X_j of a column in U
	unproc, order, def []int32
	// pending forward-elimination sources of each binary row, as linked
	// lists: pendHead[row], then pendNext[entry] (-1 ends); pendSrc[entry]
	pendHead, pendNext, pendSrc []int32
}

// run solves the reduced system of ph, reusing the memory of p2.
func (p2 *phase2) run(ph *phase1) error {
	p2.uParts(ph)
	words, candBits, hd := p2.words, p2.candBits, p2.hd
	u := len(ph.uCols)
	nb := len(p2.cand)
	H := len(hd)
	p2.nb = nb
	p2.colRow = resize(p2.colRow, u)
	p2.rowCol = filled(p2.rowCol, nb+H, -1)
	ops := p2.ops[:0]
	args := p2.args[:0]
	p2.pendHead = filled(p2.pendHead, nb, -1)
	pendNext, pendSrc := p2.pendNext[:0], p2.pendSrc[:0]

	// Forward GF(2) elimination over the binary rows. A column without a
	// binary pivot is deferred to the HDPC rows. An unprocessed row never
	// has a bit in an earlier column: pivot columns were eliminated from it
	// and deferred columns had no unprocessed row with that bit.
	//
	// A pivot row's right-hand side does not change after it is processed
	// (during this pass), so the XORs into a row are not recorded one by
	// one: they are collected and emitted as a single fused operation when
	// the row itself becomes a pivot, and never if it does not.
	unproc := resize(p2.unproc, nb)
	for i := range unproc {
		unproc[i] = int32(i)
	}
	order, deferred := p2.order[:0], p2.def[:0]
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
		if e := p2.pendHead[r]; e >= 0 {
			a0 := int32(len(args))
			for ; e >= 0; e = pendNext[e] {
				args = append(args, pendSrc[e])
			}
			ops = append(ops, p2op{kind: p2XorN, dst: r, a0: a0, a1: int32(len(args))})
		}
		rb := candBits[int(r)*words : int(r+1)*words]
		for _, t := range unproc {
			tb := candBits[int(t)*words : int(t+1)*words]
			if tb[w]&bit != 0 {
				for k := w; k < words; k++ {
					tb[k] ^= rb[k]
				}
				pendNext = append(pendNext, p2.pendHead[t])
				pendSrc = append(pendSrc, r)
				p2.pendHead[t] = int32(len(pendSrc) - 1)
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
			p2.ops, p2.args, p2.unproc, p2.order, p2.def = ops, args, unproc[:0], order, deferred
			p2.pendNext, p2.pendSrc = pendNext, pendSrc
			return ErrSingular
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
	for _, r := range slices.Backward(order) {
		c := int(p2.rowCol[r])
		rb := candBits[int(r)*words : int(r+1)*words]
		a0 := int32(len(args))
		for i, x := range rb {
			for ; x != 0; x &= x - 1 {
				if j := i<<6 + bits.TrailingZeros64(x); j != c {
					args = append(args, p2.colRow[j])
				}
			}
		}
		if a1 := int32(len(args)); a1 > a0 {
			ops = append(ops, p2op{kind: p2XorN, dst: r, a0: a0, a1: a1})
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
	p2.ops, p2.args, p2.unproc, p2.order, p2.def = live, args, unproc[:0], order, deferred
	p2.pendNext, p2.pendSrc = pendNext, pendSrc
	return nil
}

// uParts forward-substitutes the phase-1 pivot rows through each other and
// expresses every remaining row in terms of U only: it computes the U parts
// A'_p of the pivot rows (bitsets of p2.words 64-bit words each), the
// unchosen non-HDPC rows with their reduced U parts, and the H reduced HDPC
// rows as dense GF(256) rows of length u.
func (p2 *phase2) uParts(ph *phase1) {
	rs := ph.rs
	u := len(ph.uCols)
	words := (u + 63) / 64
	p2.words = words
	pivBits := zeroed(p2.pivBits, len(ph.pivRow)*words)
	p2.pivBits = pivBits
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

	cand := p2.cand[:0]
	for r := range rs.nrows() {
		if ph.vdeg[r] != chosenRow {
			cand = append(cand, int32(r))
		}
	}
	p2.cand = cand
	candBits := zeroed(p2.candBits, len(cand)*words)
	p2.candBits = candBits
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

	p2.hdpcRows(ph)
}

// hdpcRows computes the reduced HDPC rows G_HDPC * X + I_H, where row j of X
// is A'_q if column j is the pivot of q, or the unit vector of column j if it
// is in U. G_HDPC = MT * GAMMA is applied with the recurrence
// z_j = alpha*z_{j-1} + X_j (GAMMA) followed by scattering z_j into the rows
// of MT that have a nonzero in column j.
func (p2 *phase2) hdpcRows(ph *phase1) {
	p := ph.rs.p
	u := len(ph.uCols)
	H, n := p.H, p.KPrime+p.S
	pairs := hdpcPairs(p)
	pivBits, words := p2.pivBits, p2.words

	// Rows of u coefficients: the H HDPC rows and z.
	buf := zeroed(p2.hdBytes, (H+1)*u)
	p2.hdBytes = buf
	hd := resize(p2.hd, H)
	p2.hd = hd
	for h := range hd {
		hd[h] = buf[h*u : (h+1)*u : (h+1)*u]
	}
	z := buf[H*u : (H+1)*u]
	unit := resize(p2.unitBits, words)
	p2.unitBits = unit
	// X returns X_j as a bitset of u bits.
	X := func(j int) []uint64 {
		if q := int(ph.colPiv[j]); q >= 0 {
			return pivBits[q*words : (q+1)*words]
		}
		clear(unit)
		k := ph.uIdx[j]
		unit[k>>6] = 1 << (k & 63)
		return unit
	}
	for j := range n - 1 {
		gf256.HDPCStepBits(z, X(j), hd[pairs.r1[j]], hd[pairs.r2[j]])
	}
	// The last column: z = alpha*z + X_{n-1}, and MT holds alpha^h in row h.
	gf256.MulSlice(z, z, 2)
	for i, w := range X(n - 1) {
		for ; w != 0; w &= w - 1 {
			z[i<<6+bits.TrailingZeros64(w)] ^= 1
		}
	}
	for h := range H {
		gf256.MulAddSlice(hd[h], z, gf256.Exp(h))
		hd[h][ph.uIdx[n+h]] ^= 1
	}
}

func xorWords(dst, src []uint64) {
	src = src[:len(dst)]
	for i := range dst {
		dst[i] ^= src[i]
	}
}

type mtPairs struct{ r1, r2 []uint8 }

var hdpcPairCache syncCache[*mtPairs]

func hdpcPairs(p *rfc.Params) *mtPairs {
	if m, ok := hdpcPairCache.load(p.KPrime); ok {
		return m
	}
	r1, r2 := p.HDPCPairs()
	return hdpcPairCache.loadOrStore(p.KPrime, &mtPairs{r1, r2})
}
