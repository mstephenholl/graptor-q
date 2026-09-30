// Package refsolve is a deliberately naive reference implementation of the
// RFC 6330 systematic encoder: the constraint matrix A is built densely with
// the literal loops of Section 5.3.3.3 (including an explicit MT x GAMMA
// product), and A*C = D is solved by plain Gauss-Jordan elimination using only
// scalar field operations.
//
// It is slow (cubic in L) and exists only as a test oracle for the optimized
// solver, so that a bug in the sparse solver or in a SIMD kernel cannot hide
// behind itself.
package refsolve

import (
	"errors"

	"github.com/mholland/graptorq/internal/gf256"
	"github.com/mholland/graptorq/internal/rfc"
)

// ErrSingular is returned when A does not have full column rank.
var ErrSingular = errors.New("refsolve: constraint matrix is rank deficient")

// Matrix returns the dense constraint matrix of Section 5.3.3.4.2: S LDPC
// rows, H HDPC rows, then one LT row per internal symbol ID in isis. Each row
// has L entries.
func Matrix(p *rfc.Params, isis []uint32) [][]byte {
	K, S, H, W, L, P, B := p.KPrime, p.S, p.H, p.W, p.L, p.P, p.B
	A := make([][]byte, S+H+len(isis))
	for i := range A {
		A[i] = make([]byte, L)
	}

	// LDPC: D[i] = C[B+i] initially, then the two loops of Section 5.3.3.3.
	for i := 0; i < S; i++ {
		A[i][B+i] ^= 1
	}
	for i := 0; i < B; i++ {
		a := 1 + i/S
		b := i % S
		A[b][i] ^= 1
		b = (b + a) % S
		A[b][i] ^= 1
		b = (b + a) % S
		A[b][i] ^= 1
	}
	for i := 0; i < S; i++ {
		a := i % P
		b := (i + 1) % P
		A[i][W+a] ^= 1
		A[i][W+b] ^= 1
	}

	// HDPC: G_HDPC = MT * GAMMA, followed by I_H.
	n := K + S
	MT := make([][]byte, H)
	for i := range MT {
		MT[i] = make([]byte, n)
	}
	for j := 0; j < n-1; j++ {
		r := rfc.Rand(uint32(j+1), 6, uint32(H))
		MT[r][j] = 1
		MT[(r+rfc.Rand(uint32(j+1), 7, uint32(H-1))+1)%uint32(H)][j] = 1
	}
	for i := 0; i < H; i++ {
		MT[i][n-1] = gf256.Exp(i)
	}
	for h := 0; h < H; h++ {
		row := A[S+h]
		for i := 0; i < n; i++ { // GAMMA[i][j] = alpha^(i-j) for i >= j
			if MT[h][i] == 0 {
				continue
			}
			for j := 0; j <= i; j++ {
				row[j] ^= gf256.Mul(MT[h][i], gf256.Exp(i-j))
			}
		}
		row[n+h] ^= 1
	}

	// LT rows: the coefficients of Enc[K', C, Tuple[K', X]].
	for r, x := range isis {
		row := A[S+H+r]
		t := p.Tuple(x)
		for _, c := range encIndices(p, t) {
			row[c] ^= 1
		}
	}
	return A
}

// encIndices is the literal Enc pseudo-code of Section 5.3.5.3, returning the
// intermediate symbol indices it adds together.
func encIndices(p *rfc.Params, t rfc.Tuple) []int {
	W, P, P1 := uint32(p.W), uint32(p.P), uint32(p.P1)
	d, a, b, d1, a1, b1 := t.D, t.A, t.B, t.D1, t.A1, t.B1
	idx := []int{int(b)}
	for j := uint32(1); j <= d-1; j++ {
		b = (b + a) % W
		idx = append(idx, int(b))
	}
	for b1 >= P {
		b1 = (b1 + a1) % P1
	}
	idx = append(idx, int(W+b1))
	for j := uint32(1); j <= d1-1; j++ {
		b1 = (b1 + a1) % P1
		for b1 >= P {
			b1 = (b1 + a1) % P1
		}
		idx = append(idx, int(W+b1))
	}
	return idx
}

// Solve solves A*C = D for the L columns of A by Gauss-Jordan elimination,
// returning the L intermediate symbols. A and D are not modified.
func Solve(A [][]byte, D [][]byte) ([][]byte, error) {
	if len(A) == 0 {
		return nil, ErrSingular
	}
	L := len(A[0])
	a := make([][]byte, len(A))
	d := make([][]byte, len(D))
	for i := range A {
		a[i] = append([]byte(nil), A[i]...)
		d[i] = append([]byte(nil), D[i]...)
	}
	for col := 0; col < L; col++ {
		piv := -1
		for r := col; r < len(a); r++ {
			if a[r][col] != 0 {
				piv = r
				break
			}
		}
		if piv < 0 {
			return nil, ErrSingular
		}
		a[col], a[piv] = a[piv], a[col]
		d[col], d[piv] = d[piv], d[col]
		if inv := gf256.Inv(a[col][col]); inv != 1 {
			scale(a[col], inv)
			scale(d[col], inv)
		}
		for r := range a {
			if r == col || a[r][col] == 0 {
				continue
			}
			f := a[r][col]
			addScaled(a[r], a[col], f)
			addScaled(d[r], d[col], f)
		}
	}
	return d[:L], nil
}

// Residual reports whether A*C = D holds exactly.
func Residual(A [][]byte, C [][]byte, D [][]byte) bool {
	for r := range A {
		acc := make([]byte, len(D[r]))
		for c, v := range A[r] {
			if v != 0 {
				addScaled(acc, C[c], v)
			}
		}
		for i := range acc {
			if acc[i] != D[r][i] {
				return false
			}
		}
	}
	return true
}

// Encode computes the intermediate symbols for a source block of K' symbols
// (source symbols followed by zero padding) of size T.
func Encode(p *rfc.Params, source [][]byte) ([][]byte, error) {
	isis := make([]uint32, p.KPrime)
	for i := range isis {
		isis[i] = uint32(i)
	}
	A := Matrix(p, isis)
	T := len(source[0])
	D := make([][]byte, p.S+p.H, len(A))
	for i := range D {
		D[i] = make([]byte, T)
	}
	D = append(D, source...)
	return Solve(A, D)
}

// Symbol returns the encoding symbol with internal symbol ID isi,
// Enc[K', C, Tuple[K', isi]].
func Symbol(p *rfc.Params, C [][]byte, isi uint32) []byte {
	out := make([]byte, len(C[0]))
	for _, i := range encIndices(p, p.Tuple(isi)) {
		addScaled(out, C[i], 1)
	}
	return out
}

func scale(row []byte, c byte) {
	for i := range row {
		row[i] = gf256.Mul(row[i], c)
	}
}

func addScaled(dst, src []byte, c byte) {
	for i := range dst {
		dst[i] ^= gf256.Mul(src[i], c)
	}
}
