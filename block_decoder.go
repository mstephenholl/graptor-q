package graptorq

import (
	"github.com/mholland/graptorq/internal/gf256"
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

	// Memory reused by every decode, so that decoding after Reset does not
	// allocate.
	work []byte // intermediate symbols
	ws   solver.Workspace
	scr  struct {
		isis, want, missing []uint32
		in                  [][]byte
		units, ck, buf      []byte
		rows, rhs           [][]byte
	}
}

// resize returns s with length n, reusing its memory when it is large
// enough. The contents are unspecified.
func resize[T any](s []T, n int) []T {
	if cap(s) < n {
		return make([]T, n)
	}
	return s[:n]
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

	if d.useLowLoss() && d.decodeLowLoss(out) {
		d.out, d.decoded = out, true
		return nil
	}

	p := d.p
	s := &d.scr
	isis, in := s.isis[:0], s.in[:0]
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
	missing := s.missing[:0]
	for i, off := range d.srcOff {
		if off < 0 {
			missing = append(missing, uint32(i))
		}
	}
	s.isis, s.in, s.missing = isis, in, missing

	// Only the intermediate symbols needed to rebuild the missing source
	// symbols are computed.
	plan, err := d.ws.NewPartialPlan(p, isis, missing)
	if err != nil {
		return &DecodeError{SBN: d.sbn, Received: d.Received(), Needed: d.k}
	}
	if n := plan.Slots * d.t; cap(d.work) < n {
		d.work = make([]byte, n)
	}
	work := d.work[:plan.Slots*d.t]
	plan.ExecuteParallel(work, d.t, in, d.o.concurrency)
	var cols [48]uint16
	for i, off := range d.srcOff {
		dst := out[i*d.t : (i+1)*d.t]
		if off >= 0 {
			copy(dst, d.sym(off))
		} else {
			solver.EncodeSymbol(p, work, d.t, uint32(i), dst, cols[:0])
		}
	}
	d.out, d.decoded = out, true
	return nil
}

// lowLossMode forces (1) or disables (-1) the low-loss decoding path; zero
// chooses by cost. It is a test hook.
var lowLossMode = 0

// useLowLoss reports whether decodeLowLoss is expected to be faster than
// building a decoding plan. The cached encoder plan replaces the symbolic
// solve, at the price of a dense m x m solve for the m missing symbols.
func (d *BlockDecoder) useLowLoss() bool {
	if d.o.planCache == nil || lowLossMode < 0 {
		return false
	}
	if lowLossMode > 0 {
		return true
	}
	return lowLossWorthIt(d.p.KPrime, d.t, d.k-d.nsrc)
}

func lowLossWorthIt(kPrime, t, m int) bool {
	// The dense solve costs about m*m*(m+t) byte operations; the symbolic
	// solve it saves is linear in K'. Measured break-even points
	// (BenchmarkDecodePaths, one P-core) lie between 2400*K' and 9600*K'
	// for K' from 100 to 50000 and T from 64 to 1280; 2000*K' stays below
	// all of them.
	return int64(m)*int64(m)*int64(m+t) <= 2000*int64(kPrime)
}

// decodeLowLoss recovers the m missing source symbols with the cached
// encoder plan instead of a decoding plan. The intermediate symbols are a
// linear function of the K' extended source symbols, so with C0 computed
// from the received source symbols (missing ones set to zero) and C_k the
// response to a unit value of missing symbol k, every repair symbol j gives
// the equation
//
//	y_j - Enc_j(C0) = sum over k of Enc_j(C_k) * s_k.
//
// The responses C_k are computed together by running the plan on symbols of
// m bytes, input k being the k-th unit vector. The resulting m-unknown dense
// system is solvable exactly when the full decoding system is. It returns
// false if the equations are singular (the caller then uses the full solver,
// which also uses every received symbol).
func (d *BlockDecoder) decodeLowLoss(out []byte) bool {
	p, T := d.p, d.t
	full, err := d.o.planCache.plan(p)
	if err != nil {
		return false
	}
	// A few equations beyond m make a singular system very unlikely; the
	// fallback to the full solver uses all of them. Only the intermediate
	// symbols these repair symbols need are computed.
	m := d.k - d.nsrc
	r := min(len(d.repESI), m+20)
	shift := uint32(p.KPrime - d.k)
	s := &d.scr
	want := resize(s.want, r)
	s.want = want
	for j, esi := range d.repESI[:r] {
		want[j] = esi + shift
	}
	plan := d.ws.Prune(full, want)

	missing := s.missing[:0]
	in := resize(s.in, p.KPrime)
	clear(in) // padding symbols stay nil (zero)
	for i, off := range d.srcOff {
		if off >= 0 {
			in[i] = d.sym(off)
		} else {
			missing = append(missing, uint32(i))
		}
	}
	s.missing, s.in = missing, in

	if n := plan.Slots * T; cap(d.work) < n {
		d.work = make([]byte, n)
	}
	c0 := d.work[:plan.Slots*T]
	plan.ExecuteParallel(c0, T, in, d.o.concurrency)

	clear(in)
	units := resize(s.units, m*m)
	clear(units)
	s.units = units
	for k, i := range missing {
		in[i] = units[k*m : (k+1)*m]
		in[i][k] = 1
	}
	ck := resize(s.ck, plan.Slots*m)
	s.ck = ck
	plan.Execute(ck, m, in)

	buf := resize(s.buf, r*(m+T))
	rows, rhs := resize(s.rows, r), resize(s.rhs, r)
	s.buf, s.rows, s.rhs = buf, rows, rhs
	var cols [48]uint16
	for j, isi := range want {
		rows[j] = buf[j*(m+T) : j*(m+T)+m]
		rhs[j] = buf[j*(m+T)+m : (j+1)*(m+T)]
		solver.EncodeSymbol(p, ck, m, isi, rows[j], cols[:0])
		solver.EncodeSymbol(p, c0, T, isi, rhs[j], cols[:0])
		gf256.AddSlice(rhs[j], d.sym(d.repOff[j]))
	}
	pivots, err := d.ws.SolveDense(rows, rhs, m)
	if err != nil {
		return false
	}
	for i, off := range d.srcOff {
		if off >= 0 {
			copy(out[i*T:(i+1)*T], d.sym(off))
		}
	}
	for k, i := range missing {
		copy(out[int(i)*T:int(i+1)*T], rhs[pivots[k]])
	}
	return true
}

// AppendSource appends the decoded block (length bytes) to dst.
func (d *BlockDecoder) AppendSource(dst []byte) ([]byte, error) {
	if !d.decoded {
		return dst, ErrNotDecoded
	}
	return append(dst, d.out[:d.length]...), nil
}
