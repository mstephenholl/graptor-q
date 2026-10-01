package main

import (
	"math"
	"strings"
	"testing"
)

func TestMedianInterval(t *testing.T) {
	for _, c := range []struct{ n, lo, hi int }{
		{6, 0, 5},   // too few rounds to exclude any: the whole range
		{10, 1, 8},  // [x(2), x(9)]: 97.9%
		{20, 5, 14}, // [x(6), x(15)]: 95.9%
		{30, 9, 20}, // [x(10), x(21)]: 95.7%
	} {
		if lo, hi := medianInterval(c.n); lo != c.lo || hi != c.hi {
			t.Errorf("medianInterval(%d) = %d, %d; want %d, %d", c.n, lo, hi, c.lo, c.hi)
		}
	}
}

// samples builds one case with the given head/base ratios per round.
func samples(ratios ...float64) *Samples {
	var v [2][]float64
	for _, r := range ratios {
		v[0] = append(v[0], 1000)
		v[1] = append(v[1], 1000*r)
	}
	return &Samples{Rounds: len(ratios), NsPerOp: map[string][2][]float64{"BenchmarkGate/x": v}}
}

func repeat(r float64, n int) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = r
	}
	return s
}

func TestCompare(t *testing.T) {
	// 10% slower in every round.
	if r := Compare(samples(repeat(1.10, 20)...), 0.05)[0]; r.Verdict != Regressed || math.Abs(r.Ratio-1.10) > 1e-9 {
		t.Errorf("uniform 10%% slowdown: %+v", r)
	}
	// 10% slower in 14 rounds and faster in 6: the median is above the
	// threshold but the interval reaches below zero.
	mixed := append(repeat(1.10, 14), repeat(0.97, 6)...)
	if r := Compare(samples(mixed...), 0.05)[0]; r.Verdict != Same {
		t.Errorf("14 of 20 slower: %+v", r)
	}
	// 3% slower every round: significant, but under the threshold.
	if r := Compare(samples(repeat(1.03, 20)...), 0.05)[0]; r.Verdict != Same {
		t.Errorf("uniform 3%% slowdown: %+v", r)
	}
	// One wild round does not move the median.
	wild := append(repeat(1.0, 19), 3.0)
	if r := Compare(samples(wild...), 0.05)[0]; r.Verdict != Same || r.Ratio != 1 {
		t.Errorf("one outlier: %+v", r)
	}
	if r := Compare(samples(repeat(0.8, 20)...), 0.05)[0]; r.Verdict != Improved {
		t.Errorf("uniform 20%% speedup: %+v", r)
	}
}

func TestExactPattern(t *testing.T) {
	got := exactPattern("BenchmarkGate/op=encode/K=100/T=1280")
	want := `^BenchmarkGate$/^op=encode$/^K=100$/^T=1280$`
	if got != want {
		t.Errorf("exactPattern = %s, want %s", got, want)
	}
}

func TestParseBench(t *testing.T) {
	out := `goos: linux
cpu: AMD EPYC 7763 64-Core Processor
BenchmarkGate/op=encode/K=100/T=1280         	    5016	     47935 ns/op	2670.28 MB/s
BenchmarkGate/op=muladd/tier=gfni/n=1280            	12550494	        19.23 ns/op	66557.91 MB/s
PASS
`
	res, cpu, err := parseBench(strings.NewReader(out))
	if err != nil || cpu != "AMD EPYC 7763 64-Core Processor" || len(res) != 2 ||
		res["BenchmarkGate/op=encode/K=100/T=1280"] != 47935 || res["BenchmarkGate/op=muladd/tier=gfni/n=1280"] != 19.23 {
		t.Errorf("parseBench = %v, %q, %v", res, cpu, err)
	}
}
