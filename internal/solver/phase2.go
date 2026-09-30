package solver

import (
	"encoding/binary"
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

	// scratch
	words              int      // 64-bit words per U bitset
	pivBits, candBits  []uint64 // U parts of the pivot and candidate rows
	hd                 [][]byte // reduced HDPC rows
	hdWords            []uint64
	hdBytes            []byte
	unproc, order, def []int32
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

	// Forward GF(2) elimination over the binary rows. A column without a
	// binary pivot is deferred to the HDPC rows. An unprocessed row never
	// has a bit in an earlier column: pivot columns were eliminated from it
	// and deferred columns had no unprocessed row with that bit.
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
			p2.ops, p2.unproc, p2.order, p2.def = ops, unproc[:0], order, deferred
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
	p2.ops, p2.unproc, p2.order, p2.def = live, unproc[:0], order, deferred
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
		if !ph.chosen[r] {
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

	// The recurrence runs on 64-bit words holding 8 coefficients each (byte
	// k of the row is byte k%8 of word k/8, little-endian), so that each
	// step is one pass: multiplication by alpha is a shift and a conditional
	// XOR with the low byte of the polynomial.
	uw := (u + 7) / 8
	wbuf := zeroed(p2.hdWords, (H+1)*uw)
	p2.hdWords = wbuf
	z := wbuf[H*uw:]
	for j := range n - 1 {
		for i, x := range z {
			hi := x & 0x8080808080808080
			z[i] = (x&^hi)<<1 ^ (hi>>7)*(gf256.Poly&0xFF)
		}
		if q := int(ph.colPiv[j]); q >= 0 {
			// Bit k of the bitset becomes byte k of z: each bitset byte
			// spreads into one word of z.
			for i, x := range pivBits[q*words : (q+1)*words] {
				zw := z[i*8 : min(i*8+8, uw)]
				for t := range zw {
					zw[t] ^= spreadBits[byte(x>>(8*t))]
				}
			}
		} else {
			k := int(ph.uIdx[j])
			z[k>>3] ^= 1 << (k & 7 * 8)
		}
		h1 := wbuf[int(pairs.r1[j])*uw : int(pairs.r1[j]+1)*uw]
		h2 := wbuf[int(pairs.r2[j])*uw : int(pairs.r2[j]+1)*uw]
		for i, x := range z {
			h1[i] ^= x
			h2[i] ^= x
		}
	}

	buf := resize(p2.hdBytes, (H+1)*uw*8)
	p2.hdBytes = buf
	for i, x := range wbuf {
		binary.LittleEndian.PutUint64(buf[i*8:], x)
	}
	hd := resize(p2.hd, H)
	p2.hd = hd
	for h := range hd {
		hd[h] = buf[h*uw*8 : h*uw*8+u : h*uw*8+u]
	}
	// The last column: z = alpha*z + X_{n-1}, and MT holds alpha^h in row h.
	zb := buf[H*uw*8 : H*uw*8+u]
	gf256.MulSlice(zb, zb, 2)
	if q := int(ph.colPiv[n-1]); q >= 0 {
		for i, x := range pivBits[q*words : (q+1)*words] {
			for ; x != 0; x &= x - 1 {
				zb[i<<6+bits.TrailingZeros64(x)] ^= 1
			}
		}
	} else {
		zb[ph.uIdx[n-1]] ^= 1
	}
	for h := range H {
		gf256.MulAddSlice(hd[h], zb, gf256.Exp(h))
		hd[h][ph.uIdx[n+h]] ^= 1
	}
}

// spreadBits[b] has byte i equal to bit i of b.
var spreadBits = func() (t [256]uint64) {
	for b := range t {
		for i := range 8 {
			t[b] |= uint64(b>>i&1) << (8 * i)
		}
	}
	return
}()

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
