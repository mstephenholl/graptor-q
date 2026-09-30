package refsolve

import (
	"bytes"
	"encoding/hex"
	"math/rand/v2"
	"testing"

	"github.com/mholland/graptorq/internal/rfc"
	"github.com/mholland/graptorq/internal/testutil"
)

// sourceSymbols splits data into K' symbols of size T (zero padded).
func sourceSymbols(p *rfc.Params, data []byte, T int) [][]byte {
	src := make([][]byte, p.KPrime)
	for i := range src {
		src[i] = make([]byte, T)
		if off := i * T; off < len(data) {
			copy(src[i], data[off:])
		}
	}
	return src
}

func TestKnownAnswerVectors(t *testing.T) {
	for _, v := range testutil.BlockVectors() {
		K := (len(v.Data) + v.SymbolSize - 1) / v.SymbolSize
		if K > 1100 {
			continue // too large for the cubic reference solver
		}
		if K > 100 && testing.Short() {
			continue
		}
		p, err := rfc.ForK(K)
		if err != nil {
			t.Fatal(err)
		}
		src := sourceSymbols(p, v.Data, v.SymbolSize)
		C, err := Encode(p, src)
		if err != nil {
			t.Fatalf("%s K=%d: %v", v.Name, K, err)
		}
		for _, s := range v.Symbols {
			isi := s.ESI
			if int(s.ESI) >= K {
				isi = s.ESI + uint32(p.KPrime-K)
			}
			got := Symbol(p, C, isi)
			if hex.EncodeToString(got) != s.Hex {
				t.Errorf("%s K=%d ESI=%d: got %x, want %s", v.Name, K, s.ESI, got, s.Hex)
			}
		}
	}
}

// Every K' must give an invertible A for ISIs 0..K'-1 (Section 5.6), the
// solution must satisfy A*C = D, and the code must be systematic.
func TestSmallKPrimeFullRank(t *testing.T) {
	limit := 400
	if testing.Short() {
		limit = 120
	}
	rng := rand.New(rand.NewPCG(3, 4))
	for i := range rfc.All() {
		p := &rfc.All()[i]
		if p.KPrime > limit {
			break
		}
		src := make([][]byte, p.KPrime)
		for j := range src {
			src[j] = []byte{byte(rng.Uint32()), byte(rng.Uint32())}
		}
		isis := make([]uint32, p.KPrime)
		for j := range isis {
			isis[j] = uint32(j)
		}
		A := Matrix(p, isis)
		D := make([][]byte, p.S+p.H)
		for j := range D {
			D[j] = make([]byte, 2)
		}
		D = append(D, src...)
		C, err := Solve(A, D)
		if err != nil {
			t.Fatalf("K'=%d: %v", p.KPrime, err)
		}
		if !Residual(A, C, D) {
			t.Fatalf("K'=%d: A*C != D", p.KPrime)
		}
		for j, s := range src {
			if !bytes.Equal(Symbol(p, C, uint32(j)), s) {
				t.Fatalf("K'=%d: Enc(C, %d) != source symbol", p.KPrime, j)
			}
		}
	}
}

func TestSingular(t *testing.T) {
	p, _ := rfc.ForK(10)
	// Only K'-1 LT rows: A has fewer rows than columns.
	isis := make([]uint32, p.KPrime-1)
	for i := range isis {
		isis[i] = uint32(i)
	}
	A := Matrix(p, isis)
	D := make([][]byte, len(A))
	for i := range D {
		D[i] = []byte{0}
	}
	if _, err := Solve(A, D); err != ErrSingular {
		t.Fatalf("err = %v, want ErrSingular", err)
	}
}
