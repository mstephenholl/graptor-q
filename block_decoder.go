package graptorq

import (
	"github.com/mholland/graptorq/internal/rfc"
	"github.com/mholland/graptorq/internal/solver"
)

// BlockDecoder recovers a single source block from encoding symbols.
//
// Add symbols with AddSymbol until Decode succeeds; a failed Decode (an error
// wrapping ErrInsufficientSymbols) can be retried after adding more symbols.
// A BlockDecoder is not safe for concurrent use.
type BlockDecoder struct {
	p      *rfc.Params
	k, t   int
	length int
	o      options
	sbn    uint8 // reported in DecodeError

	arena   []byte  // received symbol data
	srcOff  []int32 // per source ESI: offset in arena, or -1
	nsrc    int
	repESI  []uint32
	repOff  []int32
	seenRep map[uint32]struct{}

	decoded bool
	out     []byte // K*T bytes of decoded source symbols
	work    []byte // intermediate symbols, kept for reuse after Reset
}

// NewBlockDecoder returns a decoder for a source block of length bytes split
// into symbols of symbolSize bytes.
func NewBlockDecoder(length, symbolSize int, opts ...Option) (*BlockDecoder, error) {
	return newBlockDecoder(length, symbolSize, 0, buildOptions(opts))
}

func newBlockDecoder(length, symbolSize int, sbn uint8, o options) (*BlockDecoder, error) {
	if symbolSize < 1 {
		return nil, &ParamError{"symbol size", uint64(max(symbolSize, 0)), "must be positive"}
	}
	if length < 1 {
		return nil, &ParamError{"block length", uint64(max(length, 0)), "must be positive"}
	}
	k := (length + symbolSize - 1) / symbolSize
	p, err := checkBlock(k, symbolSize)
	if err != nil {
		return nil, err
	}
	if m := o.maxMemory; m > 0 && int64(p.L+1+k)*int64(symbolSize) > m {
		return nil, ErrMemoryLimit
	}
	d := &BlockDecoder{p: p, k: k, t: symbolSize, length: length, o: o, sbn: sbn}
	d.srcOff = make([]int32, k)
	d.Reset()
	return d, nil
}

// K returns the number of source symbols.
func (d *BlockDecoder) K() int { return d.k }

// SymbolSize returns the symbol size T.
func (d *BlockDecoder) SymbolSize() int { return d.t }

// Received returns the number of distinct encoding symbols added.
func (d *BlockDecoder) Received() int { return d.nsrc + len(d.repESI) }

// Decoded reports whether the block has been decoded.
func (d *BlockDecoder) Decoded() bool { return d.decoded }

// Reset discards all received symbols and any decoded data, keeping the
// allocated memory for reuse.
func (d *BlockDecoder) Reset() {
	for i := range d.srcOff {
		d.srcOff[i] = -1
	}
	d.arena = d.arena[:0]
	d.nsrc = 0
	d.repESI = d.repESI[:0]
	d.repOff = d.repOff[:0]
	clear(d.seenRep)
	d.decoded = false
	d.out = d.out[:0]
}

// AddSymbol adds the encoding symbol with the given ESI, which must be
// exactly SymbolSize bytes; the data is copied. It reports whether the
// symbol was new (duplicates, and symbols arriving after decoding, are
// ignored).
func (d *BlockDecoder) AddSymbol(esi uint32, sym []byte) (added bool, err error) {
	if esi > MaxESI {
		return false, ErrESIRange
	}
	if len(sym) != d.t {
		return false, ErrSymbolSize
	}
	if d.decoded {
		return false, nil
	}
	if m := d.o.maxMemory; m > 0 && int64(d.p.L+1+len(d.arena)/d.t+1)*int64(d.t) > m {
		return false, ErrMemoryLimit
	}
	off := int32(len(d.arena))
	if int(esi) < d.k {
		if d.srcOff[esi] >= 0 {
			return false, nil
		}
		d.srcOff[esi] = off
		d.nsrc++
	} else {
		if d.seenRep == nil {
			d.seenRep = make(map[uint32]struct{})
		}
		if _, dup := d.seenRep[esi]; dup {
			return false, nil
		}
		d.seenRep[esi] = struct{}{}
		d.repESI = append(d.repESI, esi)
		d.repOff = append(d.repOff, off)
	}
	d.arena = append(d.arena, sym...)
	return true, nil
}

func (d *BlockDecoder) sym(off int32) []byte {
	return d.arena[off : int(off)+d.t : int(off)+d.t]
}

// Decode recovers the source block. It returns an error wrapping
// ErrInsufficientSymbols if more symbols are needed.
func (d *BlockDecoder) Decode() error {
	if d.decoded {
		return nil
	}
	if d.Received() < d.k {
		return &DecodeError{SBN: d.sbn, Received: d.Received(), Needed: d.k}
	}
	out := grow(d.out[:0], d.k*d.t)
	if d.nsrc == d.k {
		for i, off := range d.srcOff {
			copy(out[i*d.t:], d.sym(off))
		}
		d.out, d.decoded = out, true
		return nil
	}

	p := d.p
	n := d.nsrc + (p.KPrime - d.k) + len(d.repESI)
	isis := make([]uint32, 0, n)
	in := make([][]byte, 0, n)
	for i, off := range d.srcOff {
		if off >= 0 {
			isis = append(isis, uint32(i))
			in = append(in, d.sym(off))
		}
	}
	for x := d.k; x < p.KPrime; x++ { // padding symbols are known zeros
		isis = append(isis, uint32(x))
		in = append(in, nil)
	}
	shift := uint32(p.KPrime - d.k)
	for i, esi := range d.repESI {
		isis = append(isis, esi+shift)
		in = append(in, d.sym(d.repOff[i]))
	}
	missing := make([]uint32, 0, d.k-d.nsrc)
	for i, off := range d.srcOff {
		if off < 0 {
			missing = append(missing, uint32(i))
		}
	}

	// Only the intermediate symbols needed to rebuild the missing source
	// symbols are computed.
	plan, err := solver.NewPartialPlan(p, isis, missing)
	if err != nil {
		return &DecodeError{SBN: d.sbn, Received: d.Received(), Needed: d.k}
	}
	if n := plan.Slots * d.t; cap(d.work) < n {
		d.work = make([]byte, n)
	}
	work := d.work[:plan.Slots*d.t]
	plan.ExecuteParallel(work, d.t, in, d.o.concurrency)
	var cols []uint16
	for i, off := range d.srcOff {
		dst := out[i*d.t : (i+1)*d.t]
		if off >= 0 {
			copy(dst, d.sym(off))
		} else {
			cols = solver.EncodeSymbol(p, work, d.t, uint32(i), dst, cols)
		}
	}
	d.out, d.decoded = out, true
	return nil
}

// AppendSource appends the decoded block (length bytes) to dst.
func (d *BlockDecoder) AppendSource(dst []byte) ([]byte, error) {
	if !d.decoded {
		return dst, ErrNotDecoded
	}
	return append(dst, d.out[:d.length]...), nil
}
