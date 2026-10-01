package solver

import (
	"github.com/mstephenholl/graptor-q/internal/gf256"
	"github.com/mstephenholl/graptor-q/internal/rfc"
)

// EncodeSymbol writes Enc[K', C, Tuple[K', isi]] (Section 5.3.5.3) to dst,
// where C is the array of intermediate symbols of size T stored contiguously
// in work. cols is scratch space and is returned for reuse.
func EncodeSymbol(p *rfc.Params, work []byte, T int, isi uint32, dst []byte, cols []uint16) []uint16 {
	cols = p.AppendEncCols(cols[:0], isi)
	var buf [48][]byte // at most d + d1 = 30 + 3 operands
	srcs := buf[:0]
	for _, c := range cols {
		srcs = append(srcs, work[int(c)*T:int(c+1)*T])
	}
	gf256.SetXor(dst[:T], srcs) // one pass over the operands
	return cols
}
