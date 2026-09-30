package solver

import "github.com/mholland/graptorq/internal/gf256"

// SolveDense solves a small dense system over GF(256) by Gauss-Jordan
// elimination. rows[j] holds the m coefficients of equation j and rhs[j] its
// right-hand side symbol; both are modified in place. On success, for each
// unknown k, rhs[pivots[k]] holds the value of unknown k. It returns
// ErrSingular if the equations do not determine all m unknowns. Equations
// beyond the m pivots are not checked for consistency.
func SolveDense(rows, rhs [][]byte, m int) (pivots []int, err error) {
	used := make([]bool, len(rows))
	pivots = make([]int, m)
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
