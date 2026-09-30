package graptorq

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math/rand/v2"
	"sync"
	"testing"

	"github.com/mholland/graptorq/internal/solver"
	"github.com/mholland/graptorq/internal/testutil"
)

// sendSymbol passes the symbol with the given ESI from enc to dec, failing
// the test on any error. It reports whether dec stored the symbol.
func sendSymbol(t testing.TB, enc *BlockEncoder, dec *BlockDecoder, esi uint32) bool {
	t.Helper()
	sym, err := enc.AppendSymbol(nil, esi)
	if err != nil {
		t.Fatalf("AppendSymbol(%d): %v", esi, err)
	}
	added, err := dec.AddSymbol(esi, sym)
	if err != nil {
		t.Fatalf("AddSymbol(%d): %v", esi, err)
	}
	return added
}

// mustAdd adds a symbol to dec, failing the test on error.
func mustAdd(t testing.TB, dec *BlockDecoder, esi uint32, sym []byte) {
	t.Helper()
	if _, err := dec.AddSymbol(esi, sym); err != nil {
		t.Fatalf("AddSymbol(%d): %v", esi, err)
	}
}

func TestBlockEncoderVectors(t *testing.T) {
	for _, v := range testutil.BlockVectors() {
		enc, err := NewBlockEncoder(v.Data, v.SymbolSize)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range v.Symbols {
			var got []byte
			if s.ESI > MaxESI { // xssnick's vector uses a 32-bit ID
				got, err = enc.appendISI(nil, s.ESI+uint32(enc.KPrime()-enc.K()))
			} else {
				got, err = enc.AppendSymbol(nil, s.ESI)
			}
			if err != nil {
				t.Fatal(err)
			}
			if hex.EncodeToString(got) != s.Hex {
				t.Errorf("%s K=%d ESI=%d: got %x, want %s", v.Name, enc.K(), s.ESI, got, s.Hex)
			}
		}
	}
}

// Decode symbols produced by cberner/raptorq: the "hello" block from its two
// repair symbols alone, and a K=50 block with one source symbol replaced by
// eight repair symbols.
func TestBlockDecoderIndependentSymbols(t *testing.T) {
	hello := testutil.BlockVectors()[1]
	dec, err := NewBlockDecoder(len(hello.Data), hello.SymbolSize)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range hello.Symbols[:2] {
		b, _ := hex.DecodeString(s.Hex)
		if _, err := dec.AddSymbol(s.ESI, b); err != nil {
			t.Fatal(err)
		}
	}
	if err := dec.Decode(); err != nil {
		t.Fatal(err)
	}
	if got, _ := dec.AppendSource(nil); !bytes.Equal(got, hello.Data) {
		t.Fatalf("decoded %q", got)
	}

	const K, T = 50, 8
	data := testutil.VectorData(K * T)
	dec, _ = NewBlockDecoder(len(data), T)
	for i := range K {
		if i != 7 {
			mustAdd(t, dec, uint32(i), data[i*T:(i+1)*T])
		}
	}
	for i, h := range testutil.TransitionRepairs {
		b, _ := hex.DecodeString(h)
		mustAdd(t, dec, uint32(K+i), b)
	}
	if err := dec.Decode(); err != nil {
		t.Fatal(err)
	}
	if got, _ := dec.AppendSource(nil); !bytes.Equal(got, data) {
		t.Fatal("K=50 block decoded incorrectly")
	}
}

func TestBlockRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 12))
	trials := 300
	if testing.Short() {
		trials = 60
	}
	failuresAtK := 0
	for trial := range trials {
		T := 1 + rng.IntN(64)
		K := 1 + rng.IntN(300)
		if trial%10 == 0 {
			K = 1 + rng.IntN(3000)
		}
		length := (K-1)*T + 1 + rng.IntN(T)
		data := make([]byte, length)
		for i := range data {
			data[i] = byte(rng.Uint32())
		}
		enc, err := NewBlockEncoder(data, T)
		if err != nil {
			t.Fatal(err)
		}
		if enc.K() != K {
			t.Fatalf("K = %d, want %d", enc.K(), K)
		}
		dec, err := NewBlockDecoder(length, T)
		if err != nil {
			t.Fatal(err)
		}
		// Drop each source symbol with probability loss, then add repair
		// symbols (with random gaps) until decoding succeeds.
		loss := rng.Float64()
		for i := range K {
			if rng.Float64() >= loss {
				sendSymbol(t, enc, dec, uint32(i))
			}
		}
		esi := uint32(K + rng.IntN(1000))
		for dec.Received() < K {
			sendSymbol(t, enc, dec, esi)
			esi += 1 + uint32(rng.IntN(3))
		}
		attempts := 0
		for {
			err := dec.Decode()
			if err == nil {
				break
			}
			if !errors.Is(err, ErrInsufficientSymbols) {
				t.Fatal(err)
			}
			if attempts == 0 {
				failuresAtK++
			}
			if attempts++; attempts > 10 {
				t.Fatalf("K=%d: still failing after %d extra symbols", K, attempts)
			}
			sendSymbol(t, enc, dec, esi)
			esi++
		}
		got, err := dec.AppendSource(nil)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("trial %d K=%d T=%d: round trip mismatch (%v)", trial, K, T, err)
		}
	}
	// RFC 6330 promises < 1% failure with K symbols; allow statistical slack.
	if failuresAtK > trials/20 {
		t.Errorf("%d/%d decodes needed more than K symbols", failuresAtK, trials)
	}
}

func TestBlockErrors(t *testing.T) {
	if _, err := NewBlockEncoder(nil, 8); !errors.Is(err, ErrInvalidParameters) {
		t.Errorf("empty block: %v", err)
	}
	if _, err := NewBlockEncoder(make([]byte, 10), 0); !errors.Is(err, ErrInvalidParameters) {
		t.Errorf("zero symbol size: %v", err)
	}
	if _, err := NewBlockEncoder(make([]byte, MaxSourceSymbols+1), 1); !errors.Is(err, ErrInvalidParameters) {
		t.Errorf("too many symbols: %v", err)
	}
	if _, err := NewBlockEncoder(make([]byte, 1000), 10, WithMaxMemory(1000)); !errors.Is(err, ErrMemoryLimit) {
		t.Errorf("memory limit: %v", err)
	}
	enc, _ := NewBlockEncoder(make([]byte, 100), 10)
	if _, err := enc.AppendSymbol(nil, MaxESI+1); !errors.Is(err, ErrESIRange) {
		t.Errorf("ESI range: %v", err)
	}
	dec, _ := NewBlockDecoder(100, 10)
	if _, err := dec.AddSymbol(0, make([]byte, 9)); !errors.Is(err, ErrSymbolSize) {
		t.Errorf("symbol size: %v", err)
	}
	var de *DecodeError
	if err := dec.Decode(); !errors.As(err, &de) || de.Needed != 10 || !errors.Is(err, ErrInsufficientSymbols) {
		t.Errorf("decode with no symbols: %v", err)
	}
	if _, err := dec.AppendSource(nil); !errors.Is(err, ErrNotDecoded) {
		t.Errorf("AppendSource before decode: %v", err)
	}
	if added, _ := dec.AddSymbol(3, make([]byte, 10)); !added {
		t.Error("first symbol not added")
	}
	if added, _ := dec.AddSymbol(3, make([]byte, 10)); added {
		t.Error("duplicate symbol added")
	}
}

func TestBlockDecoderReset(t *testing.T) {
	data := testutil.VectorData(1000)
	enc, _ := NewBlockEncoder(data, 16)
	dec, _ := NewBlockDecoder(len(data), 16)
	for round := range 3 {
		dec.Reset()
		for esi := uint32(round); dec.Received() < enc.K()+2; esi += 2 {
			sendSymbol(t, enc, dec, esi)
		}
		if err := dec.Decode(); err != nil {
			t.Fatal(err)
		}
		if got, _ := dec.AppendSource(nil); !bytes.Equal(got, data) {
			t.Fatalf("round %d: mismatch", round)
		}
	}
}

// Encoders sharing a cache from many goroutines (run with -race).
func TestPlanCacheConcurrent(t *testing.T) {
	cache := NewPlanCache(1 << 20) // small: forces evictions
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 20 {
				k := 1 + (g*37+i*101)%400
				data := testutil.VectorData(k * 4)
				enc, err := NewBlockEncoder(data, 4, WithPlanCache(cache))
				if err != nil {
					t.Error(err)
					return
				}
				ref, _ := NewBlockEncoder(data, 4, WithoutPlanCache())
				a, _ := enc.AppendSymbol(nil, uint32(k+5))
				b, _ := ref.AppendSymbol(nil, uint32(k+5))
				if !bytes.Equal(a, b) {
					t.Error("cached plan gives a different symbol")
				}
			}
		}()
	}
	wg.Wait()
}

func TestAppendSymbolNoAlloc(t *testing.T) {
	data := testutil.VectorData(64 * 1000)
	enc, _ := NewBlockEncoder(data, 64)
	buf := make([]byte, 0, 64)
	if _, err := enc.AppendSymbol(buf, 5000); err != nil { // prepares
		t.Fatal(err)
	}
	for _, esi := range []uint32{3, 1000, 123456} {
		var err error
		if n := testing.AllocsPerRun(100, func() { _, err = enc.AppendSymbol(buf[:0], esi) }); n != 0 || err != nil {
			t.Errorf("AppendSymbol(ESI %d) allocates %.0f times (err %v)", esi, n, err)
		}
	}
}

// A decoder with a memory limit must refuse symbols beyond it rather than
// grow without bound (e.g. a flood of distinct repair ESIs).
func TestDecoderMemoryLimit(t *testing.T) {
	const K, T = 100, 64
	limit := int64(128+K+20) * T // about the working memory plus K+20 symbols
	dec, err := NewBlockDecoder(K*T, T, WithMaxMemory(limit))
	if err != nil {
		t.Fatal(err)
	}
	sym := make([]byte, T)
	var err2 error
	accepted := 0
	for esi := uint32(K); esi < K+1000; esi++ {
		if _, err2 = dec.AddSymbol(esi, sym); err2 != nil {
			break
		}
		accepted++
	}
	if !errors.Is(err2, ErrMemoryLimit) || accepted > K+20 {
		t.Fatalf("accepted %d symbols, last error %v", accepted, err2)
	}
}

// The low-loss path must succeed exactly when the full decoding system has
// full rank (checked with the solver's symbolic rank test), and then return
// the source block. Received sets with no extra symbols make singular
// systems occur regularly.
func TestLowLossPathEquivalence(t *testing.T) {
	rng := rand.New(rand.NewPCG(51, 52))
	trials, singular := 4000, 0
	if testing.Short() {
		trials = 1000
	}
	for trial := range trials {
		K := 1 + rng.IntN(60)
		T := 1 + rng.IntN(4)
		if trial%10 == 0 {
			K, T = 1+rng.IntN(600), 1+rng.IntN(100)
		}
		data := make([]byte, K*T)
		for i := range data {
			data[i] = byte(rng.Uint32())
		}
		enc, _ := NewBlockEncoder(data, T)
		dec, _ := NewBlockDecoder(len(data), T)
		p := dec.p
		m := 1 + rng.IntN(min(K, 25))
		lost := rng.Perm(K)[:m]
		isLost := make([]bool, K)
		for _, i := range lost {
			isLost[i] = true
		}
		var isis []uint32
		for i := range K {
			if !isLost[i] {
				sendSymbol(t, enc, dec, uint32(i))
				isis = append(isis, uint32(i))
			}
		}
		for x := K; x < p.KPrime; x++ {
			isis = append(isis, uint32(x))
		}
		for dec.Received() < K+rng.IntN(3) {
			esi := uint32(K + rng.IntN(1<<20))
			sym, _ := enc.AppendSymbol(nil, esi)
			if added, _ := dec.AddSymbol(esi, sym); added {
				isis = append(isis, esi+uint32(p.KPrime-K))
			}
		}
		want := solver.Solvable(p, isis)
		if got := dec.decodeLowLoss(); got != want {
			t.Fatalf("trial %d K=%d m=%d: low-loss path %v, full system solvable %v", trial, K, m, got, want)
		}
		if !want {
			singular++
			continue
		}
		dec.decoded = true
		if out, _ := dec.AppendSource(nil); !bytes.Equal(out, data) {
			t.Fatalf("trial %d K=%d m=%d: wrong data from the low-loss path", trial, K, m)
		}
	}
	if singular == 0 {
		t.Error("no singular systems: the test does not cover failures")
	}
	t.Logf("%d trials, %d singular", trials, singular)
}

// Both decoding paths through the public API, including the fallback from
// a singular low-loss system to the full solver.
func TestDecodePathsAgree(t *testing.T) {
	rng := rand.New(rand.NewPCG(53, 54))
	for _, mode := range []int{1, -1} {
		lowLossMode = mode
		for trial := range 100 {
			K, T := 1+rng.IntN(1500), 1+rng.IntN(64)
			data := make([]byte, K*T-rng.IntN(T))
			for i := range data {
				data[i] = byte(rng.Uint32())
			}
			enc, _ := NewBlockEncoder(data, T)
			dec, _ := NewBlockDecoder(len(data), T)
			loss := rng.Float64() * 0.2
			for i := range K {
				if rng.Float64() >= loss {
					sendSymbol(t, enc, dec, uint32(i))
				}
			}
			for esi := uint32(K); dec.Decode() != nil; esi++ {
				sendSymbol(t, enc, dec, esi)
			}
			if got, _ := dec.AppendSource(nil); !bytes.Equal(got, data) {
				t.Fatalf("mode %d trial %d: wrong data", mode, trial)
			}
		}
	}
	lowLossMode = 0
}

// After warm-up, decoding again after Reset must not allocate, on both
// decoding paths (with a single goroutine).
func TestDecodeNoAlloc(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are not meaningful with the race detector")
	}
	for _, c := range []struct {
		name       string
		K, T, lost int
		lowLoss    bool
	}{
		{"low-loss", 1000, 64, 5, true},
		{"full-solver", 1000, 64, 300, false},
		{"full-solver-large", 10000, 16, 1500, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			data := testutil.PatternData(c.K*c.T, 1)
			enc, _ := NewBlockEncoder(data, c.T)
			dec, _ := NewBlockDecoder(len(data), c.T, WithConcurrency(1))
			if got := lowLossWorthIt(dec.p.KPrime, c.T, c.lost); got != c.lowLoss {
				t.Fatalf("expected low-loss path %v, cost model says %v", c.lowLoss, got)
			}
			var esis []uint32
			var syms [][]byte
			for i := c.lost; i < c.K; i++ {
				s, _ := enc.AppendSymbol(nil, uint32(i))
				esis, syms = append(esis, uint32(i)), append(syms, s)
			}
			for i := range c.lost + 2 {
				s, _ := enc.AppendSymbol(nil, uint32(c.K+i))
				esis, syms = append(esis, uint32(c.K+i)), append(syms, s)
			}
			var err error
			run := func() {
				dec.Reset()
				for i, s := range syms {
					if _, e := dec.AddSymbol(esis[i], s); e != nil {
						err = e
						return
					}
				}
				err = dec.Decode()
			}
			run() // warm up buffers and the plan cache
			if run(); err != nil {
				t.Fatal(err)
			}
			if n := testing.AllocsPerRun(10, run); n != 0 || err != nil {
				t.Errorf("decode allocates %.0f times (err %v)", n, err)
			}
			if got, _ := dec.AppendSource(nil); !bytes.Equal(got, data) {
				t.Fatal("wrong data")
			}
		})
	}
}
