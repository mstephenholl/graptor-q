package solver

import (
	"math"
	"math/rand/v2"
	"os"
	"testing"

	"github.com/mholland/graptorq/internal/rfc"
)

// binomialTail returns P(X >= x) for X ~ Binomial(n, p).
func binomialTail(n, x int, p float64) float64 {
	if x <= 0 {
		return 1
	}
	var sum float64
	lp, lq := math.Log(p), math.Log1p(-p)
	for k := x; k <= n; k++ {
		lc, _ := math.Lgamma(float64(n + 1))
		a, _ := math.Lgamma(float64(k + 1))
		b, _ := math.Lgamma(float64(n - k + 1))
		term := math.Exp(lc - a - b + float64(k)*lp + float64(n-k)*lq)
		sum += term
		if term < sum*1e-12 {
			break
		}
	}
	return sum
}

// TestRecoveryProperties checks the compliance requirements of RFC 6330
// Section 5.8: for a source block of K' symbols, receiving K' + overhead
// encoding symbols with ESIs drawn uniformly from the whole ESI range must
// fail to decode at most 1 in 100 times (overhead 0), 1 in 10^4 (overhead 1)
// and 1 in 10^6 (overhead 2).
//
// Decoding succeeds exactly when the constraint matrix has full rank, which
// Solvable decides without any symbol data. The test fails only on strong
// evidence (binomial tail probability below 1e-3) that a failure rate
// exceeds its bound. Set GRAPTORQ_LONG=1 for many more trials and all K'.
func TestRecoveryProperties(t *testing.T) {
	long := os.Getenv("GRAPTORQ_LONG") != ""
	maxK, trials := 1500, []int{3000, 3000, 0}
	if testing.Short() {
		maxK, trials = 500, []int{1000, 1000, 0}
	}
	if long {
		maxK, trials = rfc.MaxKPrime, []int{100_000, 300_000, 1_000_000}
	}
	var kps []*rfc.Params
	for i := range rfc.All() {
		if p := &rfc.All()[i]; p.KPrime <= maxK {
			kps = append(kps, p)
		}
	}
	bounds := []float64{1e-2, 1e-4, 1e-6}
	rng := rand.New(rand.NewPCG(41, 42))
	for overhead, n := range trials {
		if n == 0 {
			continue
		}
		failures := 0
		for range n {
			// Small blocks dominate the trials; large ones are costly.
			p := kps[rng.IntN(len(kps))]
			if long && rng.IntN(20) != 0 {
				p = kps[rng.IntN(min(len(kps), 150))]
			}
			seen := make(map[uint32]struct{}, p.KPrime+overhead)
			isis := make([]uint32, 0, p.KPrime+overhead)
			for len(isis) < p.KPrime+overhead {
				x := uint32(rng.IntN(1 << 24)) // K = K', so ISI = ESI
				if _, dup := seen[x]; !dup {
					seen[x] = struct{}{}
					isis = append(isis, x)
				}
			}
			if !Solvable(p, isis) {
				failures++
			}
		}
		tail := binomialTail(n, failures, bounds[overhead])
		t.Logf("overhead %d: %d/%d failures (%.2e; bound %.0e; P(>= observed | bound) = %.3g)",
			overhead, failures, n, float64(failures)/float64(n), bounds[overhead], tail)
		if tail < 1e-3 {
			t.Errorf("overhead %d: failure rate %d/%d significantly exceeds %.0e", overhead, failures, n, bounds[overhead])
		}
	}
}
