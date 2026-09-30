package solver

// SolveDense solves a small dense system over GF(256) by Gauss-Jordan
// elimination. rows[j] holds the m coefficients of equation j and rhs[j] its
// right-hand side symbol; both are modified in place. On success, for each
// unknown k, rhs[pivots[k]] holds the value of unknown k. It returns
// ErrSingular if the equations do not determine all m unknowns. Equations
// beyond the m pivots are not checked for consistency.
func SolveDense(rows, rhs [][]byte, m int) (pivots []int, err error) {
	var w Workspace
	return w.SolveDense(rows, rhs, m)
}
