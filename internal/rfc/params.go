// Package rfc implements the parameter tables and pseudo-random generators of
// RFC 6330 Section 5: Table 2 lookups, Rand, Deg, Tuple and the index patterns
// of the LT, LDPC and HDPC constraint rows.
package rfc

//go:generate go run ./gentables -in ../../testdata/rfc6330.txt -out tables_gen.go

import (
	"errors"
	"sort"
)

// MaxKPrime is K'_max, the largest supported extended source block size.
const MaxKPrime = 56403

// ErrTooManySymbols is returned when K exceeds K'_max.
var ErrTooManySymbols = errors.New("rfc: more than 56403 source symbols in a block")

type tableRow struct {
	kPrime, j, s, h, w uint16
}

// Params are the parameters derived from one row of Table 2 (Section 5.3.3.3).
type Params struct {
	KPrime int // K': extended source block size
	J      int // systematic index J(K')
	S      int // number of LDPC symbols
	H      int // number of HDPC symbols
	W      int // number of LT symbols
	L      int // K' + S + H: number of intermediate symbols
	P      int // L - W: number of permanently inactivated symbols
	P1     int // smallest prime >= P
	U      int // P - H
	B      int // W - S

	tupleA, tupleB uint32 // Tuple constants A and B (Section 5.3.5.4)
}

var params [len(systematic)]Params

func init() {
	for i, r := range systematic {
		p := Params{
			KPrime: int(r.kPrime), J: int(r.j), S: int(r.s), H: int(r.h), W: int(r.w),
		}
		p.L = p.KPrime + p.S + p.H
		p.P = p.L - p.W
		p.P1 = p.P
		for !isPrime(p.P1) {
			p.P1++
		}
		p.U = p.P - p.H
		p.B = p.W - p.S
		a := uint32(53591 + p.J*997)
		if a%2 == 0 {
			a++
		}
		p.tupleA = a
		p.tupleB = uint32(10267 * (p.J + 1))
		params[i] = p
	}
}

// ForK returns the parameters for the smallest K' in Table 2 with K' >= k.
func ForK(k int) (*Params, error) {
	if k < 1 {
		return nil, errors.New("rfc: source block must have at least one symbol")
	}
	i := sort.Search(len(params), func(i int) bool { return params[i].KPrime >= k })
	if i == len(params) {
		return nil, ErrTooManySymbols
	}
	return &params[i], nil
}

// All returns the parameters for every row of Table 2, in increasing K'
// order. The returned slice must not be modified.
func All() []Params { return params[:] }

// Index returns the Table 2 row number of p (0-based).
func (p *Params) Index() int {
	return sort.Search(len(params), func(i int) bool { return params[i].KPrime >= p.KPrime })
}

func isPrime(n int) bool {
	if n < 2 {
		return false
	}
	for d := 2; d*d <= n; d++ {
		if n%d == 0 {
			return false
		}
	}
	return true
}
