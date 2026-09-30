package rfc

import "slices"

// AppendEncCols appends the intermediate-symbol indices combined by the
// encoding symbol generator Enc[K', C, Tuple[K', x]] (Section 5.3.5.3): the d
// LT columns in [0, W) followed by the d1 PI columns in [W, L).
//
// The indices are distinct for every ISI and every row of Table 2 (see
// TestEncColsDistinctProof), so the row is the plain sum of these symbols.
func (p *Params) AppendEncCols(dst []uint16, x uint32) []uint16 {
	return p.AppendTupleCols(dst, p.Tuple(x))
}

// AppendTupleCols is AppendEncCols for a precomputed tuple.
func (p *Params) AppendTupleCols(dst []uint16, t Tuple) []uint16 {
	w, pp, p1 := uint32(p.W), uint32(p.P), uint32(p.P1)
	b := t.B
	dst = append(dst, uint16(b))
	for j := uint32(1); j < t.D; j++ {
		b = (b + t.A) % w
		dst = append(dst, uint16(b))
	}
	b1 := t.B1
	for b1 >= pp {
		b1 = (b1 + t.A1) % p1
	}
	dst = append(dst, uint16(w+b1))
	for j := uint32(1); j < t.D1; j++ {
		b1 = (b1 + t.A1) % p1
		for b1 >= pp {
			b1 = (b1 + t.A1) % p1
		}
		dst = append(dst, uint16(w+b1))
	}
	return dst
}

// LDPCRows returns the S LDPC constraint rows of Section 5.3.3.3 as sorted
// column lists: G_LDPC,1 entries in [0, B), the identity entry B+i, and the
// two G_LDPC,2 entries in [W, L). Entries that would be added twice cancel.
func (p *Params) LDPCRows() [][]uint16 {
	rows := make([][]uint16, p.S)
	for i := range rows {
		rows[i] = append(rows[i], uint16(p.B+i))
	}
	for i := 0; i < p.B; i++ {
		a := 1 + i/p.S
		b := i % p.S
		rows[b] = append(rows[b], uint16(i))
		b = (b + a) % p.S
		rows[b] = append(rows[b], uint16(i))
		b = (b + a) % p.S
		rows[b] = append(rows[b], uint16(i))
	}
	for i := 0; i < p.S; i++ {
		a := i % p.P
		b := (i + 1) % p.P
		rows[i] = append(rows[i], uint16(p.W+a), uint16(p.W+b))
	}
	for i := range rows {
		rows[i] = CancelPairs(rows[i])
	}
	return rows
}

// HDPCPairs returns, for each column j in [0, K'+S-1) of the H x (K'+S)
// matrix MT (Section 5.3.3.3), the two distinct rows holding a one. The last
// column K'+S-1 is not included: it holds alpha^i in row i.
func (p *Params) HDPCPairs() (r1, r2 []uint8) {
	n := p.KPrime + p.S - 1
	r1 = make([]uint8, n)
	r2 = make([]uint8, n)
	h := uint32(p.H)
	for j := range n {
		a := Rand(uint32(j+1), 6, h)
		r1[j] = uint8(a)
		r2[j] = uint8((a + Rand(uint32(j+1), 7, h-1) + 1) % h)
	}
	return r1, r2
}

// CancelPairs sorts cols and removes indices that occur an even number of
// times, leaving each odd-count index once. This gives GF(2) sum semantics.
func CancelPairs(cols []uint16) []uint16 {
	slices.Sort(cols)
	out := cols[:0]
	for i := 0; i < len(cols); {
		j := i + 1
		for j < len(cols) && cols[j] == cols[i] {
			j++
		}
		if (j-i)%2 == 1 {
			out = append(out, cols[i])
		}
		i = j
	}
	return out
}
