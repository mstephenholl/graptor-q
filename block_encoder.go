package graptorq

import (
	"fmt"
	"math"
	"sync"

	"github.com/mholland/graptorq/internal/rfc"
	"github.com/mholland/graptorq/internal/solver"
)

// Limits of RFC 6330.
const (
	MaxSourceSymbols  = 56403        // K'_max: maximum symbols in a source block
	MaxSourceBlocks   = 255          // maximum number of source blocks (Z)
	MaxTransferLength = 942574504275 // maximum object size F (erratum 5548)
	MaxESI            = 1<<24 - 1    // maximum encoding symbol ID
	OTISize           = 12           // size of the encoded OTI
	PayloadIDSize     = 4            // size of the encoded FEC Payload ID
	maxSymbolSize     = 1<<16 - 1    // T is a 16-bit field
)

// checkBlock returns the parameters of a source block of k symbols of size t.
// It rejects blocks whose working memory, (L+1)*t bytes, cannot be
// addressed with int (which only happens on 32-bit platforms); after this
// check, every symbol offset computed as index*t fits in int.
func checkBlock(k, t int) (*rfc.Params, error) {
	p, err := rfc.ForK(k)
	if err != nil {
		return nil, &ParamError{"source symbols", uint64(k), fmt.Sprintf("more than %d", MaxSourceSymbols)}
	}
	if int64(p.L+1)*int64(t) > int64(math.MaxInt) {
		return nil, ErrMemoryLimit
	}
	return p, nil
}

// BlockEncoder generates the encoding symbols of a single source block.
//
// Source symbols (ESI < K) are the block data itself; repair symbols (ESI >=
// K) are computed from the intermediate symbols, which are derived on the
// first request for a repair symbol or by Prepare. After Prepare returns,
// a BlockEncoder is safe for concurrent use.
type BlockEncoder struct {
	p    *rfc.Params
	k, t int
	src  []byte
	tail []byte // zero-padded copy of a partial last symbol
	o    options

	once sync.Once
	err  error
	work []byte // intermediate symbols C[0..L-1], T bytes each
}

// NewBlockEncoder returns an encoder for the source block src, split into
// K = ceil(len(src)/symbolSize) source symbols; a partial last symbol is
// zero-padded. src is retained without copying and must not be modified
// while the encoder is in use.
func NewBlockEncoder(src []byte, symbolSize int, opts ...Option) (*BlockEncoder, error) {
	if symbolSize < 1 {
		return nil, &ParamError{"symbol size", uint64(max(symbolSize, 0)), "must be positive"}
	}
	if len(src) == 0 {
		return nil, &ParamError{"block length", 0, "must be positive"}
	}
	k := (len(src) + symbolSize - 1) / symbolSize
	p, err := checkBlock(k, symbolSize)
	if err != nil {
		return nil, err
	}
	e := &BlockEncoder{p: p, k: k, t: symbolSize, src: src, o: buildOptions(opts)}
	if r := len(src) % symbolSize; r != 0 {
		e.tail = make([]byte, symbolSize)
		copy(e.tail, src[len(src)-r:])
	}
	if m := e.o.maxMemory; m > 0 && int64(p.L+1)*int64(symbolSize) > m {
		return nil, ErrMemoryLimit
	}
	return e, nil
}

// K returns the number of source symbols.
func (e *BlockEncoder) K() int { return e.k }

// KPrime returns K', the extended source block size used for coding.
func (e *BlockEncoder) KPrime() int { return e.p.KPrime }

// SymbolSize returns the symbol size T.
func (e *BlockEncoder) SymbolSize() int { return e.t }

// source returns source symbol i < K, zero-padded to T bytes.
func (e *BlockEncoder) source(i int) []byte {
	if off := i * e.t; off+e.t <= len(e.src) {
		return e.src[off : off+e.t : off+e.t]
	}
	return e.tail
}

// Prepare computes the intermediate symbols. It is called implicitly by the
// first repair symbol request.
func (e *BlockEncoder) Prepare() error {
	e.once.Do(func() {
		plan, err := e.o.encodingPlan(e.p)
		if err != nil {
			e.err = err
			return
		}
		in := make([][]byte, e.p.KPrime) // padding symbols (ISI >= K) stay nil
		for i := range e.k {
			in[i] = e.source(i)
		}
		e.work = make([]byte, plan.Slots*e.t)
		plan.ExecuteParallel(e.work, e.t, in, e.o.concurrency)
	})
	return e.err
}

// AppendSymbol appends the encoding symbol with the given ESI (T bytes) to
// dst and returns the extended slice.
func (e *BlockEncoder) AppendSymbol(dst []byte, esi uint32) ([]byte, error) {
	if esi > MaxESI {
		return dst, ErrESIRange
	}
	if int(esi) < e.k {
		return append(dst, e.source(int(esi))...), nil
	}
	return e.appendISI(dst, esi+uint32(e.p.KPrime-e.k))
}

// appendISI appends Enc[K', C, Tuple[K', isi]] for any 32-bit internal
// symbol ID (tests use it for vectors beyond the 24-bit ESI range).
func (e *BlockEncoder) appendISI(dst []byte, isi uint32) ([]byte, error) {
	if err := e.Prepare(); err != nil {
		return dst, err
	}
	n := len(dst)
	dst = grow(dst, e.t)
	var cols [48]uint16 // at most d + d1 = 30 + 3 columns
	solver.EncodeSymbol(e.p, e.work, e.t, isi, dst[n:], cols[:0])
	return dst, nil
}

// grow extends dst by n bytes (contents unspecified) and returns it.
func grow(dst []byte, n int) []byte {
	if cap(dst)-len(dst) < n {
		nd := make([]byte, len(dst), 2*cap(dst)+n)
		copy(nd, dst)
		dst = nd
	}
	return dst[:len(dst)+n]
}
