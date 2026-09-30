package solver

import (
	"testing"

	"github.com/mholland/graptorq/internal/rfc"
)

// Phase 1 heuristics only affect speed, but a regression that inactivates
// many more columns makes phase 2 (quadratic in u) much slower. Measured
// values at the time of writing: K'=1002: 17, 10017: 43, 56403: 160.
func TestInactivationBudget(t *testing.T) {
	for _, k := range []int{101, 1002, 10017, 30037, 56403} {
		if testing.Short() && k > 10017 {
			continue
		}
		p, _ := rfc.ForK(k)
		ph := runPhase1(newRowSet(p, seqISIs(p.KPrime)))
		extra := len(ph.uCols) - p.P
		if budget := 20 + p.KPrime/200; extra > budget {
			t.Errorf("K'=%d: %d columns inactivated beyond P, budget %d", p.KPrime, extra, budget)
		}
	}
}
