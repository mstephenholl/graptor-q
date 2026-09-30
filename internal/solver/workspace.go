package solver

import (
	"github.com/mholland/graptorq/internal/gf256"
	"github.com/mholland/graptorq/internal/rfc"
)

// Workspace holds the scratch memory for building plans, so that repeated
// solves (such as a decoder decoding one block after another) do not
// allocate. Plans returned by a Workspace share its memory and are valid
// only until its next use. The zero value is ready to use. A Workspace must
// not be used concurrently.
type Workspace struct {
	rs     rowSet
	ph     phase1
	p2     phase2
	plan   Plan
	pruned Plan
	need   []bool
	keep   []bool
	cols   []uint16
	used   []bool
	pivots []int
}

// NewPartialPlan is like the package-level NewPartialPlan, but the plan uses
// the workspace's memory.
func (w *Workspace) NewPartialPlan(p *rfc.Params, isis, want []uint32) (*Plan, error) {
	if err := w.solve(p, isis); err != nil {
		return nil, err
	}
	w.plan.assemble(&w.ph, &w.p2, len(isis))
	if want == nil {
		return &w.plan, nil
	}
	return w.Prune(&w.plan, want), nil
}

// Solvable reports whether the constraint matrix for isis has full rank.
func (w *Workspace) Solvable(p *rfc.Params, isis []uint32) bool {
	return w.solve(p, isis) == nil
}

func (w *Workspace) solve(p *rfc.Params, isis []uint32) error {
	if len(isis) < p.KPrime {
		return ErrSingular
	}
	w.rs.build(p, isis)
	w.ph.run(&w.rs)
	return w.p2.run(&w.ph)
}

// Prune is like Plan.Prune, but the result uses the workspace's memory. pl
// must not be a plan returned by an earlier call to w.Prune.
func (w *Workspace) Prune(pl *Plan, want []uint32) *Plan {
	p := pl.Params
	need := zeroed(w.need, pl.Slots)
	w.need = need
	for _, x := range want {
		w.cols = p.AppendEncCols(w.cols[:0], x)
		for _, c := range w.cols {
			need[c] = true
		}
	}
	_, tail := pl.program()
	if !pl.hasTail {
		tail = pl.instrs[pl.n4:]
	}
	keep := zeroed(w.keep, len(tail))
	w.keep = keep
	kept := 0
	for i := len(tail) - 1; i >= 0; i-- {
		if ins := &tail[i]; need[ins.dst] {
			keep[i] = true
			kept++
			for _, a := range pl.args[ins.a0:ins.a1] {
				need[a] = true
			}
		}
	}
	// The pruned plan shares the instructions before N4 and keeps its own
	// tail, so pruning does not copy the whole plan.
	kt := resize(w.pruned.tail, kept)[:0]
	for i, k := range keep {
		if k {
			kt = append(kt, tail[i])
		}
	}
	w.pruned = *pl
	w.pruned.tail, w.pruned.hasTail = kt, true
	return &w.pruned
}

// SolveDense is like the package-level SolveDense, reusing the workspace's
// memory for its bookkeeping. The returned slice is valid until the next use.
func (w *Workspace) SolveDense(rows, rhs [][]byte, m int) (pivots []int, err error) {
	used := zeroed(w.used, len(rows))
	w.used = used
	pivots = resize(w.pivots, m)
	w.pivots = pivots
	for k := range m {
		p := -1
		for j, row := range rows {
			if !used[j] && row[k] != 0 {
				p = j
				break
			}
		}
		if p < 0 {
			return nil, ErrSingular
		}
		used[p] = true
		pivots[k] = p
		// Columns before k are already zero in every row.
		if c := rows[p][k]; c != 1 {
			inv := gf256.Inv(c)
			gf256.MulSlice(rows[p][k:], rows[p][k:], inv)
			gf256.MulSlice(rhs[p], rhs[p], inv)
		}
		for j, row := range rows {
			if c := row[k]; j != p && c != 0 {
				gf256.MulAddSlice(row[k:], rows[p][k:], c)
				gf256.MulAddSlice(rhs[j], rhs[p], c)
			}
		}
	}
	return pivots, nil
}

// resize returns s with length n, reusing its memory when it is large
// enough. The contents are unspecified.
func resize[T any](s []T, n int) []T {
	if cap(s) < n {
		return make([]T, n)
	}
	return s[:n]
}

// zeroed is resize with every element set to the zero value.
func zeroed[T any](s []T, n int) []T {
	s = resize(s, n)
	clear(s)
	return s
}

// filled is resize with every element set to v.
func filled[T any](s []T, n int, v T) []T {
	s = resize(s, n)
	for i := range s {
		s[i] = v
	}
	return s
}
