package solver

import (
	"bytes"
	"encoding/hex"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/mholland/graptorq/internal/gf256"
	"github.com/mholland/graptorq/internal/refsolve"
	"github.com/mholland/graptorq/internal/rfc"
	"github.com/mholland/graptorq/internal/testutil"
)

func seqISIs(n int) []uint32 {
	isis := make([]uint32, n)
	for i := range isis {
		isis[i] = uint32(i)
	}
	return isis
}

func randomSymbols(rng *rand.Rand, n, T int) [][]byte {
	s := make([][]byte, n)
	for i := range s {
		s[i] = make([]byte, T)
		for j := range s[i] {
			s[i][j] = byte(rng.Uint32())
		}
	}
	return s
}

func symbolsOf(work []byte, T, n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		out[i] = work[i*T : (i+1)*T]
	}
	return out
}

// checkResidual verifies A*C = D with scalar arithmetic: the LDPC relations,
// the HDPC relations (evaluated via MT * GAMMA * C), and the LT rows.
func checkResidual(t *testing.T, p *rfc.Params, C [][]byte, isis []uint32, in [][]byte) {
	t.Helper()
	T := len(C[0])
	acc := make([]byte, T)
	for i, row := range p.LDPCRows() {
		clear(acc)
		for _, c := range row {
			for k := range acc {
				acc[k] ^= C[c][k]
			}
		}
		if !bytes.Equal(acc, make([]byte, T)) {
			t.Fatalf("K'=%d: LDPC row %d violated", p.KPrime, i)
		}
	}
	n := p.KPrime + p.S
	r1, r2 := p.HDPCPairs()
	hd := make([][]byte, p.H)
	for h := range hd {
		hd[h] = make([]byte, T)
	}
	z := make([]byte, T)
	for j := range n {
		for k := range z {
			z[k] = gf256.Mul(z[k], 2) ^ C[j][k]
		}
		for k := range z {
			if j < n-1 {
				hd[r1[j]][k] ^= z[k]
				hd[r2[j]][k] ^= z[k]
			} else {
				for h := range hd {
					hd[h][k] ^= gf256.Mul(gf256.Exp(h), z[k])
				}
			}
		}
	}
	for h := range hd {
		for k := range z {
			if hd[h][k] != C[n+h][k] {
				t.Fatalf("K'=%d: HDPC row %d violated", p.KPrime, h)
			}
		}
	}
	var cols []uint16
	got := make([]byte, T)
	work := make([]byte, 0, len(C)*T)
	for _, c := range C {
		work = append(work, c...)
	}
	for i, x := range isis {
		cols = EncodeSymbol(p, work, T, x, got, cols)
		want := in[i]
		if want == nil {
			want = make([]byte, T)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("K'=%d: LT row for ISI %d violated", p.KPrime, x)
		}
	}
}

// Every K' in Table 2 must yield an invertible constraint matrix for the
// ISIs 0..K'-1, and the computed intermediate symbols must satisfy A*C = D.
func TestEncodingPlanAllKPrime(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	all := rfc.All()
	for i := range all {
		p := &all[i]
		if testing.Short() && i%8 != 0 && i != len(all)-1 {
			continue
		}
		isis := seqISIs(p.KPrime)
		pl, err := NewPlan(p, isis)
		if err != nil {
			t.Fatalf("K'=%d: %v", p.KPrime, err)
		}
		const T = 3
		in := randomSymbols(rng, p.KPrime, T)
		in[len(in)-1] = nil // a padding symbol
		work := make([]byte, pl.Slots*T)
		pl.Execute(work, T, in)
		checkResidual(t, p, symbolsOf(work, T, p.L), isis, in)
	}
}

// The sparse solver must agree with the dense reference solver on random
// sets of received symbols: same rank decision and same solution.
func TestDifferentialVsReference(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	limit, trials := 300, 400
	if testing.Short() {
		limit, trials = 120, 150
	}
	var kps []*rfc.Params
	for i := range rfc.All() {
		if p := &rfc.All()[i]; p.KPrime <= limit {
			kps = append(kps, p)
		}
	}
	failures := 0
	for trial := range trials {
		p := kps[rng.IntN(len(kps))]
		// K' + overhead rows chosen from source and repair ISIs, with
		// overhead in [-2, 2] so that singular systems also occur.
		n := p.KPrime + rng.IntN(5) - 2
		seen := map[uint32]bool{}
		var isis []uint32
		for len(isis) < n {
			var x uint32
			if rng.IntN(2) == 0 {
				x = uint32(rng.IntN(p.KPrime))
			} else {
				x = uint32(p.KPrime + rng.IntN(1<<20))
			}
			if !seen[x] {
				seen[x] = true
				isis = append(isis, x)
			}
		}
		const T = 2
		in := randomSymbols(rng, n, T)

		A := refsolve.Matrix(p, isis)
		D := make([][]byte, p.S+p.H)
		for i := range D {
			D[i] = make([]byte, T)
		}
		D = append(D, in...)
		// An overdetermined system needs consistent right-hand sides: take
		// the encoding symbols of a random source block, whose intermediate
		// symbols satisfy the LDPC and HDPC relations.
		if n > p.KPrime {
			src := randomSymbols(rng, p.KPrime, T)
			Cs, err := refsolve.Encode(p, src)
			if err != nil {
				t.Fatal(err)
			}
			for i := range in {
				in[i] = refsolve.Symbol(p, Cs, isis[i])
			}
			D = append(D[:p.S+p.H], in...)
		}

		want, refErr := refsolve.Solve(A, D)
		pl, err := NewPlan(p, isis)
		if (err == nil) != (refErr == nil) {
			t.Fatalf("trial %d K'=%d n=%d: solver err=%v, reference err=%v", trial, p.KPrime, n, err, refErr)
		}
		if Solvable(p, isis) != (err == nil) {
			t.Fatalf("trial %d: Solvable disagrees with NewPlan", trial)
		}
		if err != nil {
			failures++
			continue
		}
		work := make([]byte, pl.Slots*T)
		pl.Execute(work, T, in)
		for c := range p.L {
			if !bytes.Equal(work[c*T:(c+1)*T], want[c]) {
				t.Fatalf("trial %d K'=%d: C[%d] differs from reference", trial, p.KPrime, c)
			}
		}
	}
	if failures == 0 || failures == trials {
		t.Errorf("%d/%d singular trials: test is not exercising both outcomes", failures, trials)
	}
}

func TestKnownAnswerVectors(t *testing.T) {
	for _, v := range testutil.BlockVectors() {
		T := v.SymbolSize
		K := (len(v.Data) + T - 1) / T
		p, err := rfc.ForK(K)
		if err != nil {
			t.Fatal(err)
		}
		in := make([][]byte, p.KPrime)
		for i := range K {
			in[i] = make([]byte, T)
			copy(in[i], v.Data[i*T:])
		}
		pl, err := NewPlan(p, seqISIs(p.KPrime))
		if err != nil {
			t.Fatal(err)
		}
		work := make([]byte, pl.Slots*T)
		pl.Execute(work, T, in)
		got := make([]byte, T)
		for _, s := range v.Symbols {
			isi := s.ESI
			if int(isi) >= K {
				isi += uint32(p.KPrime - K)
			}
			EncodeSymbol(p, work, T, isi, got, nil)
			if hex.EncodeToString(got) != s.Hex {
				t.Errorf("%s K=%d ESI=%d: got %x, want %s", v.Name, K, s.ESI, got, s.Hex)
			}
		}
	}
}

// Decoding the cberner-generated repair symbols: K=50 with source symbol 7
// missing and 8 repair symbols (ESIs 50..57).
func TestDecodeIndependentRepairs(t *testing.T) {
	const K, T = 50, 8
	data := testutil.VectorData(K * T)
	p, _ := rfc.ForK(K)
	var isis []uint32
	var in [][]byte
	for i := range K {
		if i != 7 {
			isis = append(isis, uint32(i))
			in = append(in, data[i*T:(i+1)*T])
		}
	}
	for x := K; x < p.KPrime; x++ { // padding symbols
		isis = append(isis, uint32(x))
		in = append(in, nil)
	}
	for i, h := range testutil.TransitionRepairs {
		b, _ := hex.DecodeString(h)
		isis = append(isis, uint32(K+i+p.KPrime-K))
		in = append(in, b)
	}
	pl, err := NewPlan(p, isis)
	if err != nil {
		t.Fatal(err)
	}
	work := make([]byte, pl.Slots*T)
	pl.Execute(work, T, in)
	got := make([]byte, T)
	EncodeSymbol(p, work, T, 7, got, nil)
	if !bytes.Equal(got, data[7*T:8*T]) {
		t.Fatalf("recovered symbol 7 = %x, want %x", got, data[7*T:8*T])
	}
}

func TestExecuteRangeMatchesExecute(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 10))
	p, _ := rfc.ForK(1000)
	pl, err := NewPlan(p, seqISIs(p.KPrime))
	if err != nil {
		t.Fatal(err)
	}
	const T = 100
	in := randomSymbols(rng, p.KPrime, T)
	full := make([]byte, pl.Slots*T)
	pl.Execute(full, T, in)
	striped := make([]byte, pl.Slots*T)
	for lo := 0; lo < T; lo += 33 {
		pl.ExecuteRange(striped, T, in, lo, min(lo+33, T))
	}
	if !bytes.Equal(full[:p.L*T], striped[:p.L*T]) {
		t.Fatal("striped execution differs")
	}
}

func BenchmarkPlan(b *testing.B) {
	for _, k := range []int{1000, 10000, 56403} {
		p, _ := rfc.ForK(k)
		isis := seqISIs(p.KPrime)
		b.Run("K'="+itoa(p.KPrime), func(b *testing.B) {
			for b.Loop() {
				if _, err := NewPlan(p, isis); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func itoa(n int) string {
	var buf [20]byte
	i := len(buf)
	for {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			return string(buf[i:])
		}
	}
}

// A partial plan must produce the same encoding symbols as a full plan for
// every requested ISI, and parallel execution must match serial execution.
func TestPartialAndParallelPlans(t *testing.T) {
	rng := rand.New(rand.NewPCG(17, 18))
	for trial := range 40 {
		p, _ := rfc.ForK(1 + rng.IntN(3000))
		// Receive all but a few source symbols, plus repair symbols.
		var isis, want []uint32
		for x := range p.KPrime {
			if rng.IntN(10) == 0 {
				want = append(want, uint32(x))
			} else {
				isis = append(isis, uint32(x))
			}
		}
		for i := 0; len(isis) < p.KPrime+2; i++ {
			isis = append(isis, uint32(p.KPrime+i*3))
		}
		src := randomSymbols(rng, p.KPrime, 8)
		full, err := NewPlan(p, seqISIs(p.KPrime))
		if err != nil {
			t.Fatal(err)
		}
		ref := make([]byte, full.Slots*8)
		full.Execute(ref, 8, src)
		in := make([][]byte, len(isis))
		for i, x := range isis {
			in[i] = make([]byte, 8)
			EncodeSymbol(p, ref, 8, x, in[i], nil)
		}

		part, err := NewPartialPlan(p, isis, want)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		const T = 2048 + 40 // several stripes plus an unaligned tail
		bigIn := make([][]byte, len(in))
		for i := range in {
			bigIn[i] = bytes.Repeat(in[i], T/8+1)[:T]
		}
		serial := make([]byte, part.Slots*T)
		part.Execute(serial, T, bigIn)
		par := make([]byte, part.Slots*T)
		part.ExecuteParallel(par, T, bigIn, 3)
		got, gotPar := make([]byte, T), make([]byte, T)
		for _, x := range want {
			EncodeSymbol(p, serial, T, x, got, nil)
			EncodeSymbol(p, par, T, x, gotPar, nil)
			if !bytes.Equal(got[:8], src[x]) || !bytes.Equal(got, gotPar) {
				t.Fatalf("trial %d K'=%d: ISI %d wrong from partial or parallel plan", trial, p.KPrime, x)
			}
		}
		if h, tl := part.program(); len(h)+len(tl) > len(full.instrs)+len(isis) {
			t.Errorf("trial %d: partial plan is not smaller (%d vs %d)", trial, len(h)+len(tl), len(full.instrs))
		}
	}
}

// BenchmarkDecodePlan builds decoding plans for 10% of the source symbols
// lost and replaced by repair symbols (plus two).
func BenchmarkDecodePlan(b *testing.B) {
	for _, k := range []int{1000, 10000, 50000} {
		p, _ := rfc.ForK(k)
		var isis, want []uint32
		for x := range p.KPrime {
			if x%10 == 3 {
				want = append(want, uint32(x))
			} else {
				isis = append(isis, uint32(x))
			}
		}
		for j := range len(want) + 2 {
			isis = append(isis, uint32(p.KPrime+j))
		}
		b.Run("K'="+itoa(p.KPrime), func(b *testing.B) {
			for b.Loop() {
				if _, err := NewPartialPlan(p, isis, want); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// samePlan reports whether two plans are the same program.
func samePlan(a, b *Plan) bool {
	ah, at := a.program()
	bh, bt := b.program()
	ai, bi := append(slices.Clone(ah), at...), append(slices.Clone(bh), bt...)
	if a.Params != b.Params || a.Slots != b.Slots || a.Inputs != b.Inputs || a.n4 != b.n4 || len(ai) != len(bi) ||
		a.hslot != b.hslot {
		return false
	}
	for _, ins := range ai {
		if ins.kind == opHDPC && !slices.Equal(a.hpiv, b.hpiv) {
			return false
		}
	}
	for i := range ai {
		x, y := ai[i], bi[i]
		if x.kind != y.kind || x.c != y.c || x.dst != y.dst || x.src != y.src ||
			!slices.Equal(a.args[x.a0:x.a1], b.args[y.a0:y.a1]) {
			return false
		}
	}
	return true
}

// A reused Workspace must behave exactly like a fresh one, whatever it was
// used for before: growing and shrinking K', full, partial and singular
// systems, and pruning of other plans.
func TestWorkspaceReuse(t *testing.T) {
	rng := rand.New(rand.NewPCG(61, 62))
	var w Workspace
	trials := 300
	if testing.Short() {
		trials = 100
	}
	singular := 0
	for trial := range trials {
		// Alternate large and small blocks so that state left by a larger
		// problem meets a smaller one and vice versa.
		k := 500 + rng.IntN(2500)
		if trial%2 == 1 {
			k = 1 + rng.IntN(600)
		}
		p, _ := rfc.ForK(k)
		// K' received symbols, or one fewer (singular), with random losses.
		n := p.KPrime + rng.IntN(3) - 1
		var isis, want []uint32
		for x := range p.KPrime {
			if rng.IntN(8) == 0 {
				want = append(want, uint32(x))
			} else {
				isis = append(isis, uint32(x))
			}
		}
		for j := 0; len(isis) < n; j++ {
			isis = append(isis, uint32(p.KPrime+j*7))
		}
		isis = isis[:n]
		if rng.IntN(3) == 0 {
			want = nil // full plan
		}

		fresh, errFresh := NewPartialPlan(p, isis, want)
		got, errGot := w.NewPartialPlan(p, isis, want)
		if (errFresh == nil) != (errGot == nil) || w.Solvable(p, isis) != (errFresh == nil) {
			t.Fatalf("trial %d K'=%d: fresh err %v, reused err %v", trial, p.KPrime, errFresh, errGot)
		}
		if errFresh != nil {
			singular++
			continue
		}
		if !samePlan(fresh, got) {
			t.Fatalf("trial %d K'=%d (partial %v): reused workspace built a different plan", trial, p.KPrime, want != nil)
		}
		if want != nil {
			enc, _ := NewPlan(p, seqISIs(p.KPrime))
			if !samePlan(enc.Prune(want), w.Prune(enc, want)) {
				t.Fatalf("trial %d: reused Prune differs", trial)
			}
		}
	}
	if singular == 0 || singular == trials {
		t.Errorf("%d/%d singular: both outcomes should occur", singular, trials)
	}
}

// NewPartialPlan reuses pooled workspaces, but the plans it returns must not
// share their memory: building more plans (another K', a pruned plan) must
// leave the earlier ones as a fresh workspace builds them.
func TestNewPlanIndependent(t *testing.T) {
	p1, _ := rfc.ForK(1000)
	p2, _ := rfc.ForK(300)
	isis1, isis2 := seqISIs(p1.KPrime), seqISIs(p2.KPrime)
	want := []uint32{3, 5, uint32(p1.KPrime + 100)}
	build := func(p *rfc.Params, isis, want []uint32) *Plan {
		t.Helper()
		pl, err := NewPartialPlan(p, isis, want)
		if err != nil {
			t.Fatal(err)
		}
		return pl
	}
	a := build(p1, isis1, nil)
	b := build(p2, isis2, nil)
	c := build(p1, isis1, want)
	build(p1, isis1, nil)
	for _, x := range []struct {
		pl         *Plan
		p          *rfc.Params
		isis, want []uint32
	}{{a, p1, isis1, nil}, {b, p2, isis2, nil}, {c, p1, isis1, want}} {
		var w Workspace
		fresh, err := w.NewPartialPlan(x.p, x.isis, x.want)
		if err != nil {
			t.Fatal(err)
		}
		if !samePlan(x.pl, fresh) {
			t.Fatalf("K'=%d want=%v: plan changed after later plans were built", x.p.KPrime, x.want)
		}
	}
}
