// Package interop cross-checks graptorq against other RaptorQ
// implementations and compares their performance:
//
//   - github.com/xssnick/raptorq (Go, single source block);
//   - github.com/takeyourhatoff/raptorq (Go, single source block), which is
//     derived from xssnick/raptorq (see its NOTICE);
//   - github.com/fgn/raptorgo (Go), an independent implementation that
//     includes the object layer (OTI, source blocks, sub-blocks);
//   - cberner/raptorq (Rust), when the RQORACLE environment variable names a
//     built tools/rqoracle binary.
package interop

import (
	"math/rand/v2"
	"testing"

	"github.com/mstephenholl/graptor-q"
	"github.com/mstephenholl/graptor-q/internal/rfc"
	"github.com/mstephenholl/graptor-q/internal/testutil"
)

// encoderCase is one source block of the encoder comparisons.
type encoderCase struct {
	p    *rfc.Params
	K, T int
	data []byte
	esis []uint32
}

// encoderCases calls f for one block per K' of RFC 6330 Table 2 (every tenth
// K' in short mode). Every other case uses the smallest K mapping to K' (so
// K != K' and the ESI-to-ISI offset is exercised), and the block length leaves
// a partial last symbol. The ESIs cover the edges of the source and repair
// ranges plus random ones.
func encoderCases(t *testing.T, seed uint64, f func(c encoderCase)) {
	t.Helper()
	sizes := []int{1, 3, 7, 13, 20, 1281}
	all := rfc.All()
	rng := rand.New(rand.NewPCG(seed, seed+1))
	for i := range all {
		p := &all[i]
		if testing.Short() && i%10 != 0 {
			continue
		}
		K := p.KPrime
		if i%2 == 1 {
			K = all[i-1].KPrime + 1
		}
		T := sizes[i%len(sizes)]
		if K > 5000 && T > 20 {
			T = 20
		}
		esis := []uint32{0, uint32(K - 1), uint32(K), uint32(K + 1), uint32(K + 100), graptorq.MaxESI}
		for range 4 {
			esis = append(esis, uint32(rng.IntN(graptorq.MaxESI+1)))
		}
		f(encoderCase{p, K, T, testutil.PatternData(K*T-T/2, uint64(i)), esis})
	}
}
