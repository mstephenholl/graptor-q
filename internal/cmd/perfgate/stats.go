package main

import (
	"cmp"
	"math"
	"slices"
)

// Verdict is the outcome for one case.
type Verdict string

const (
	Regressed Verdict = "**slower**"
	Improved  Verdict = "faster"
	Same      Verdict = ""
)

// Result compares one case. Ratios are head time over base time: above 1
// means the head is slower.
type Result struct {
	Name    string
	Ratio   float64 // exp of the median per-round log ratio
	Lo, Hi  float64 // distribution-free 95% interval of that median
	Slower  int     // rounds in which the head was slower
	Rounds  int
	Verdict Verdict
}

// Compare returns one Result per case, slowest head first. A case regresses
// when both hold:
//   - its median ratio exceeds 1+threshold (the slowdown matters), and
//   - the low end of the 95% interval of the median is above 1 (it is not
//     noise: the head was slower in clearly more than half of the rounds).
//
// The per-round ratio pairs the two runs that ran next to each other, so
// that changes of the machine's speed over the minutes of a run cancel. The
// median and its order-statistic interval (the sign test's) assume nothing
// about the distribution, and one disturbed round moves them by at most one
// position.
func Compare(s *Samples, threshold float64) []Result {
	var out []Result
	for name, v := range s.NsPerOp {
		n := min(len(v[0]), len(v[1]))
		logs := make([]float64, n)
		slower := 0
		for i := range n {
			logs[i] = math.Log(v[1][i] / v[0][i])
			if logs[i] > 0 {
				slower++
			}
		}
		slices.Sort(logs)
		lo, hi := medianInterval(n)
		r := Result{
			Name:   name,
			Ratio:  math.Exp(median(logs)),
			Lo:     math.Exp(logs[lo]),
			Hi:     math.Exp(logs[hi]),
			Slower: slower,
			Rounds: n,
		}
		switch {
		case r.Ratio > 1+threshold && r.Lo > 1:
			r.Verdict = Regressed
		case r.Ratio < 1/(1+threshold) && r.Hi < 1:
			r.Verdict = Improved
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b Result) int { return cmp.Compare(b.Ratio, a.Ratio) })
	return out
}

func median(sorted []float64) float64 {
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// medianInterval returns the 0-based indexes (lo, hi) into n sorted values
// of the narrowest order-statistic interval for the median with at least 95%
// coverage: the largest k with P(Binomial(n, 1/2) < k) <= 2.5% gives
// [x(k), x(n+1-k)] (1-based). For n = 20 that is [x(6), x(15)], with 95.9%
// coverage; its low end is above zero when the head was slower in at least
// 15 of the 20 rounds.
func medianInterval(n int) (lo, hi int) {
	k := 0
	cum := 0.0 // P(Binomial(n, 1/2) <= k-1)
	for {
		p := binom(n, k) / math.Pow(2, float64(n))
		if cum+p > 0.025 {
			break
		}
		cum += p
		k++
	}
	// k is now the largest count with P(X < k) <= 2.5%, as 1-based x(k).
	if k == 0 {
		return 0, n - 1 // too few rounds for 95%: the whole range
	}
	return k - 1, n - k
}

func binom(n, k int) float64 {
	r := 1.0
	for i := range k {
		r = r * float64(n-i) / float64(i+1)
	}
	return r
}

// Calibration summarizes repeated runs of the same comparison, normally of
// two builds that should be equally fast, to choose the threshold.
type Calibration struct {
	Runs       int
	CPUs       []string // distinct runner CPUs: hosted runners vary
	Cases      []CaseSpread
	Thresholds []ThresholdOutcome
}

type CaseSpread struct {
	Name        string
	MedianRatio float64 // median over runs of the run's median ratio
	MaxRatio    float64 // largest median ratio of any run
	MaxLo       float64 // largest low end of the interval of any run
}

type ThresholdOutcome struct {
	Threshold   float64
	FailingRuns int // runs in which at least one case would regress
}

func Calibrate(runs []*Samples, thresholds []float64) Calibration {
	c := Calibration{Runs: len(runs)}
	byCase := map[string][]Result{}
	for _, s := range runs {
		if !slices.Contains(c.CPUs, s.CPU) {
			c.CPUs = append(c.CPUs, s.CPU)
		}
		for _, r := range Compare(s, math.Inf(1)) {
			byCase[r.Name] = append(byCase[r.Name], r)
		}
	}
	for name, rs := range byCase {
		ratios := make([]float64, len(rs))
		cs := CaseSpread{Name: name}
		for i, r := range rs {
			ratios[i] = r.Ratio
			cs.MaxRatio = max(cs.MaxRatio, r.Ratio)
			cs.MaxLo = max(cs.MaxLo, r.Lo)
		}
		slices.Sort(ratios)
		cs.MedianRatio = median(ratios)
		c.Cases = append(c.Cases, cs)
	}
	slices.SortFunc(c.Cases, func(a, b CaseSpread) int { return cmp.Compare(b.MaxRatio, a.MaxRatio) })
	for _, t := range thresholds {
		o := ThresholdOutcome{Threshold: t}
		for _, s := range runs {
			if slices.ContainsFunc(Compare(s, t), func(r Result) bool { return r.Verdict == Regressed }) {
				o.FailingRuns++
			}
		}
		c.Thresholds = append(c.Thresholds, o)
	}
	return c
}
