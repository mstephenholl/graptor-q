package solver

import (
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
	var w Workspace
	pl, err := w.NewPartialPlan(p, isis, want)
	if err != nil {
		return nil, err
	}
	q := *pl // detach from the workspace, whose scratch memory can then be freed
	return &q, nil
}

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
func (pl *Plan) Size() int { return len(pl.instrs)*16 + len(pl.args)*2 + 64 }

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
		Params: p, Slots: L + 1, Inputs: inputs,
		instrs: slices.Grow(pl.instrs[:0], 2*len(ph.pivRow)+len(p2.cand)+p.H+3*n+len(p2.ops)),
		args:   slices.Grow(pl.args[:0], 2*len(rs.cols)+2*n+len(p2.ops)),
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

	// N2: right-hand sides of the used HDPC rows, G_HDPC * y.
	H := p.H
	var hslot [16]int // H <= 16 for every row of Table 2
	anyLive := false
	for h := range H {
		hslot[h] = -1
		if p2.rowCol[p2.nb+h] >= 0 {
			hslot[h] = slot(int32(p2.nb + h))
			pl.emit(opSet, 0, hslot[h], -1)
			anyLive = true
		}
	}
	if anyLive {
		pairs := hdpcPairs(p)
		started := false
		for j := range n {
			piv := ph.colPiv[j] >= 0
			switch {
			case piv && !started:
				pl.emit(opSet, 0, z, -1, uint16(j))
				started = true
			case piv:
				pl.emit(opScaleAdd, 2, z, int32(j))
			case started:
				pl.emit(opScale, 2, z, 0)
			}
			if !started {
				continue
			}
			if j < n-1 {
				for _, h := range [2]uint8{pairs.r1[j], pairs.r2[j]} {
					if hslot[h] >= 0 {
						pl.emit(opXor, 0, hslot[h], 0, uint16(z))
					}
				}
			} else {
				for h := range H {
					if hslot[h] >= 0 {
						pl.emit(opMulAdd, gf256.Exp(h), hslot[h], int32(z))
					}
				}
			}
		}
	}

	// N3: phase-2 elimination, leaving C[c] in slot c for every c in U.
	for _, op := range p2.ops {
		switch op.kind {
		case p2Xor:
			pl.emit(opXor, 0, slot(op.dst), 0, uint16(slot(op.src)))
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
// all operations are bytewise, disjoint ranges can be executed independently
// (for cache blocking or in parallel).
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
	sp := srcsPool.Get().(*[][]byte)
	srcs := (*sp)[:0] // operands of the fused XORs
	for i := range pl.instrs {
		ins := &pl.instrs[i]
		dst := sym(int(ins.dst))
		args := pl.args[ins.a0:ins.a1]
		switch ins.kind {
		case opSet:
			srcs = srcs[:0]
			if ins.src >= 0 && in[ins.src] != nil {
				srcs = append(srcs, in[ins.src][lo:hi])
			}
			for _, a := range args {
				srcs = append(srcs, sym(int(a)))
			}
			gf256.SetXor(dst, srcs)
		case opXor:
			srcs = srcs[:0]
			for _, a := range args {
				srcs = append(srcs, sym(int(a)))
			}
			gf256.XorN(dst, srcs)
		case opMulAdd:
			gf256.MulAddSlice(dst, sym(int(ins.src)), ins.c)
		case opScale:
			gf256.MulSlice(dst, dst, ins.c)
		case opScaleAdd:
			gf256.ScaleAdd(dst, sym(int(ins.src)), ins.c)
		}
	}
	clear(srcs[:cap(srcs)]) // do not keep symbol memory alive from the pool
	*sp = srcs[:0]
	srcsPool.Put(sp)
}

var srcsPool = sync.Pool{New: func() any { return new([][]byte) }}
