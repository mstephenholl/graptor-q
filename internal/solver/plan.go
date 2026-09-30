package solver

import (
	"math/bits"
	"slices"
	"sync"

	"github.com/mholland/graptorq/internal/gf256"
	"github.com/mholland/graptorq/internal/rfc"
)

type opKind uint8

const (
	opSet      opKind = iota // dst = in[src] (zero if src < 0 or nil) ^ args...
	opXor                    // dst ^= args...
	opMulAdd                 // dst ^= c * slot[src]
	opScale                  // dst = c * dst
	opScaleAdd               // dst = c*dst ^ slot[src]
	opHDPC                   // the right-hand sides of the HDPC rows (see execHDPC)
)

type instr struct {
	kind   opKind
	c      byte
	dst    uint16
	src    int32  // input index (opSet) or slot (opMulAdd, opScaleAdd)
	a0, a1 uint32 // argument slots args[a0:a1]
}

// Plan is a straight-line program that computes the L intermediate symbols
// from the right-hand sides of the constraint rows. It works on a slot array
// of Slots symbols; after execution slot c holds intermediate symbol C[c].
// Slots L, L+1 and L+2 are scratch: the HDPC recurrence value, a zero
// symbol and a sink for contributions to unused HDPC rows.
//
// A Plan is independent of the symbol size and immutable once built, so it
// may be executed concurrently and cached.
type Plan struct {
	Params *rfc.Params
	Slots  int // number of symbol slots needed: L plus one scratch slot
	Inputs int // number of input (LT row) symbols

	instrs []instr
	args   []uint16
	n4     int // index of the first instruction of the final forward substitution (N4)
	// A pruned plan shares instrs with the plan it was pruned from and runs
	// instrs[:n4] followed by tail instead of instrs[n4:].
	tail    []instr
	hasTail bool

	// For opHDPC: the slot of each used HDPC row (-1 if unused) and the set
	// of pivot columns among the first K'+S columns.
	hslot [16]int32
	hpiv  []uint64
}

// NewPlan builds the plan for the constraint matrix made of the S LDPC rows,
// the H HDPC rows and one LT row per internal symbol ID in isis. Input
// symbol i of Execute is the right-hand side of the LT row for isis[i]. It
// returns ErrSingular if the matrix does not have full rank.
func NewPlan(p *rfc.Params, isis []uint32) (*Plan, error) {
	return NewPartialPlan(p, isis, nil)
}

// NewPartialPlan is like NewPlan, but the plan is only guaranteed to compute
// the intermediate symbols needed to generate the encoding symbols with the
// internal symbol IDs in want (all of them if want is nil). A decoder that
// only lacks a few source symbols needs a small fraction of the work.
func NewPartialPlan(p *rfc.Params, isis []uint32, want []uint32) (*Plan, error) {
	w, ok := workspaces.Get().(*Workspace)
	if !ok {
		w = new(Workspace)
	}
	defer workspaces.Put(w)
	pl, err := w.NewPartialPlan(p, isis, want)
	if err != nil {
		return nil, err
	}
	q := *pl
	// The plan keeps its memory: the workspace's next plans must not reuse it.
	w.plan, w.pruned = Plan{}, Plan{}
	return &q, nil
}

// workspaces keeps the scratch memory of NewPartialPlan between calls.
var workspaces = sync.Pool{New: func() any { return new(Workspace) }}

// Prune returns a plan that only computes the intermediate symbols needed to
// generate the encoding symbols with the internal symbol IDs in want. It
// shares memory with pl, which is unchanged.
//
// Only the final forward substitution (N4) is pruned: each of its
// instructions computes one pivot column from earlier pivot columns and
// columns of U (which the preceding instructions always compute), so walking
// it backwards propagates the need for a column to everything it reads.
func (pl *Plan) Prune(want []uint32) *Plan {
	var w Workspace
	q := *w.Prune(pl, want)
	return &q
}

// Solvable reports whether the constraint matrix for isis has full rank,
// without building a plan.
func Solvable(p *rfc.Params, isis []uint32) bool {
	var w Workspace
	return w.Solvable(p, isis)
}

// Size returns the approximate memory footprint of the plan in bytes.
func (pl *Plan) Size() int { return (len(pl.instrs)+len(pl.tail))*16 + len(pl.args)*2 + 64 }

// program returns the instructions to run: head, then tail.
func (pl *Plan) program() (head, tail []instr) {
	if pl.hasTail {
		return pl.instrs[:pl.n4], pl.tail
	}
	return pl.instrs, nil
}

// emitFrom emits an instruction whose arguments were appended to pl.args
// from index a0 on.
func (pl *Plan) emitFrom(kind opKind, dst int, src int32, a0 int) {
	pl.instrs = append(pl.instrs, instr{kind: kind, dst: uint16(dst), src: src, a0: uint32(a0), a1: uint32(len(pl.args))})
}

func (pl *Plan) emit(kind opKind, c byte, dst int, src int32, args ...uint16) {
	a0 := uint32(len(pl.args))
	pl.args = append(pl.args, args...)
	pl.instrs = append(pl.instrs, instr{kind: kind, c: c, dst: uint16(dst), src: src, a0: a0, a1: uint32(len(pl.args))})
}

// assemble emits the plan into pl, reusing its memory.
func (pl *Plan) assemble(ph *phase1, p2 *phase2, inputs int) {
	rs := ph.rs
	p := rs.p
	L := p.L
	z := L // scratch slot for the HDPC recurrence
	n := p.KPrime + p.S
	*pl = Plan{
		Params: p, Slots: L + 3, Inputs: inputs,
		instrs: slices.Grow(pl.instrs[:0], 2*len(ph.pivRow)+len(p2.cand)+len(p2.ops)+1),
		args:   slices.Grow(pl.args[:0], 2*len(rs.cols)+len(p2.args)),
		hpiv:   pl.hpiv,
	}

	// slot of phase-2 row: the U column whose value it holds at the end.
	slot := func(row int32) int { return int(ph.uCols[p2.rowCol[row]]) }

	// N1: y_p = D_p + sum of y_q over the earlier pivots in row p, into slot pivCol[p].
	for i, r := range ph.pivRow {
		a0 := len(pl.args)
		for _, c := range rs.row(int(r)) {
			if q := int(ph.colPiv[c]); q >= 0 && q != i {
				pl.args = append(pl.args, c)
			}
		}
		pl.emitFrom(opSet, int(ph.pivCol[i]), rs.input(int(r)), a0)
	}

	// N2: right-hand sides of the used binary rows of the reduced system.
	for b, r := range p2.cand {
		if p2.rowCol[b] < 0 {
			continue
		}
		a0 := len(pl.args)
		for _, c := range rs.row(int(r)) {
			if ph.colPiv[c] >= 0 {
				pl.args = append(pl.args, c)
			}
		}
		pl.emitFrom(opSet, slot(int32(b)), rs.input(int(r)), a0)
	}

	// N2: right-hand sides of the used HDPC rows, G_HDPC * y, as a single
	// instruction running the GAMMA recurrence (see execHDPC).
	anyLive := false
	for h := range pl.hslot {
		pl.hslot[h] = -1
		if h < p.H && p2.rowCol[p2.nb+h] >= 0 {
			pl.hslot[h] = int32(slot(int32(p2.nb + h)))
			anyLive = true
		}
	}
	if anyLive {
		pl.hpiv = zeroed(pl.hpiv, (n+63)/64)
		for j := range n {
			if ph.colPiv[j] >= 0 {
				pl.hpiv[j>>6] |= 1 << (j & 63)
			}
		}
		pl.emit(opHDPC, 0, z, 0)
	}

	// N3: phase-2 elimination, leaving C[c] in slot c for every c in U.
	for _, op := range p2.ops {
		switch op.kind {
		case p2XorN:
			a0 := len(pl.args)
			for _, r := range p2.args[op.a0:op.a1] {
				pl.args = append(pl.args, uint16(slot(r)))
			}
			pl.emitFrom(opXor, slot(op.dst), 0, a0)
		case p2MulAdd:
			pl.emit(opMulAdd, op.c, slot(op.dst), int32(slot(op.src)))
		case p2Scale:
			pl.emit(opScale, op.c, slot(op.dst), 0)
		}
	}

	// N4: C[pivCol[p]] = D_p + sum of the other C in the original row p,
	// in pivot order (the pivot rows are unit lower triangular).
	pl.n4 = len(pl.instrs)
	for i, r := range ph.pivRow {
		a0 := len(pl.args)
		for _, c := range rs.row(int(r)) {
			if c != ph.pivCol[i] {
				pl.args = append(pl.args, c)
			}
		}
		pl.emitFrom(opSet, int(ph.pivCol[i]), rs.input(int(r)), a0)
	}
}

// Execute runs the plan. work must hold at least Slots*T bytes; in holds
// Inputs symbols of T bytes each (nil for an all-zero symbol). Afterwards
// work[c*T:(c+1)*T] is the intermediate symbol C[c].
func (pl *Plan) Execute(work []byte, T int, in [][]byte) {
	pl.ExecuteRange(work, T, in, 0, T)
}

// ExecuteParallel runs the plan on up to n goroutines, each working on a
// disjoint byte range of the symbols. Small symbols run on one goroutine.
func (pl *Plan) ExecuteParallel(work []byte, T int, in [][]byte, n int) {
	const minStripe = 512
	stripes := min(n, T/minStripe)
	if stripes <= 1 {
		pl.Execute(work, T, in)
		return
	}
	size := (T/stripes + 63) &^ 63 // keep stripes aligned for the SIMD kernels
	var wg sync.WaitGroup
	for lo := 0; lo < T; lo += size {
		hi := min(lo+size, T)
		wg.Add(1)
		go func() {
			defer wg.Done()
			pl.ExecuteRange(work, T, in, lo, hi)
		}()
	}
	wg.Wait()
}

// ExecuteRange runs the plan on bytes [lo, hi) of every symbol only. Since
// all operations are bytewise, disjoint ranges can be executed independently,
// in parallel.
//
// Running ranges one after another for cache blocking does not pay off: it
// was measured (K' from 1000 to 50000, T up to 4096, working sets from 1 MB
// to 64 MB) to be slower for every stripe width, since each stripe replays
// every instruction (16-33 ns each) while execution is bandwidth-bound
// rather than dominated by cache misses on reuse.
func (pl *Plan) ExecuteRange(work []byte, T int, in [][]byte, lo, hi int) {
	if len(in) != pl.Inputs {
		panic("solver: wrong number of input symbols")
	}
	if len(work) < pl.Slots*T { // a length check, not a read: stripes may run concurrently
		panic("solver: work buffer too small")
	}
	sym := func(s int) []byte {
		o := s * T
		return work[o+lo : o+hi : o+hi]
	}
	head, tail := pl.program()
	pl.run(head, work, T, in, lo, hi, sym)
	pl.run(tail, work, T, in, lo, hi, sym)
}

// run executes instrs over bytes [lo, hi) of every symbol.
func (pl *Plan) run(instrs []instr, work []byte, T int, in [][]byte, lo, hi int, sym func(int) []byte) {
	base := work[lo:] // slot s starts at base[s*T]
	for i := range instrs {
		ins := &instrs[i]
		o := int(ins.dst) * T
		dst := base[o : o+hi-lo : o+hi-lo]
		args := pl.args[ins.a0:ins.a1]
		switch ins.kind {
		case opSet:
			var first []byte
			if ins.src >= 0 && in[ins.src] != nil {
				first = in[ins.src][lo:hi]
			}
			gf256.XorGather(dst, first, base, T, args, false)
		case opXor:
			gf256.XorGather(dst, nil, base, T, args, true)
		case opMulAdd:
			gf256.MulAddSlice(dst, sym(int(ins.src)), ins.c)
		case opScale:
			gf256.MulSlice(dst, dst, ins.c)
		case opScaleAdd:
			gf256.ScaleAdd(dst, sym(int(ins.src)), ins.c)
		case opHDPC:
			pl.execHDPC(sym)
		}
	}
}

// execHDPC computes the right-hand sides of the used HDPC rows,
// G_HDPC * y with G_HDPC = MT * GAMMA, where y_j is the reduced right-hand
// side of pivot column j (already in slot j) and 0 for the other columns. It
// runs the recurrence z_j = alpha*z_{j-1} + y_j (GAMMA) and adds z_j to the
// two rows of MT with a one in column j, one fused kernel call per column.
// The last column of MT holds alpha^h in row h.
func (pl *Plan) execHDPC(sym func(int) []byte) {
	p := pl.Params
	H, n, L := p.H, p.KPrime+p.S, p.L
	z, zero, sink := sym(L), sym(L+1), sym(L+2)
	clear(z)
	clear(zero)
	var hs [16][]byte
	for h := range H {
		if s := pl.hslot[h]; s >= 0 {
			hs[h] = sym(int(s))
			clear(hs[h])
		} else {
			hs[h] = sink
		}
	}
	// z stays zero until the first pivot column.
	first := n
	for i, w := range pl.hpiv {
		if w != 0 {
			first = i<<6 + bits.TrailingZeros64(w)
			break
		}
	}
	pairs := hdpcPairs(p)
	for j := first; j < n; j++ {
		y := zero
		if pl.hpiv[j>>6]>>(j&63)&1 != 0 {
			y = sym(j)
		}
		if j < n-1 {
			gf256.HDPCStep(z, y, hs[pairs.r1[j]], hs[pairs.r2[j]])
		} else {
			gf256.HDPCStep(z, y, sink, sink)
			for h := range H {
				if pl.hslot[h] >= 0 {
					gf256.MulAddSlice(hs[h], z, gf256.Exp(h))
				}
			}
		}
	}
}
