package solver

import (
	"github.com/mholland/graptorq/internal/gf256"
	"github.com/mholland/graptorq/internal/rfc"
)

// EncodeSymbol writes Enc[K', C, Tuple[K', isi]] (Section 5.3.5.3) to dst,
// where C is the array of intermediate symbols of size T stored contiguously
// in work. cols is scratch space and is returned for reuse.
func EncodeSymbol(p *rfc.Params, work []byte, T int, isi uint32, dst []byte, cols []uint16) []uint16 {
	cols = p.AppendEncCols(cols[:0], isi)
	dst = dst[:T]
	copy(dst, work[int(cols[0])*T:int(cols[0]+1)*T])
	for _, c := range cols[1:] {
		gf256.AddSlice(dst, work[int(c)*T:int(c+1)*T])
	}
	return cols
}
