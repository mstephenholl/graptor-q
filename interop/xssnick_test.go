// Package interop cross-checks graptorq against other RaptorQ
// implementations: github.com/xssnick/raptorq (Go) and, when the RQORACLE
// environment variable names a built tools/rqoracle binary, cberner/raptorq
// (Rust).
package interop

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/mholland/graptorq"
	"github.com/mholland/graptorq/internal/rfc"
	"github.com/mholland/graptorq/internal/testutil"
	xraptorq "github.com/xssnick/raptorq"
)

// xssnickP1Bug reports whether xssnick v1.5.2 deviates from RFC 6330 for
// this K'. Its params.go computes P1 as the smallest prime strictly greater
// than P ("p._P1 = p._P + 1; for !isPrime(p._P1) ..."), while Section
// 5.3.3.3 defines P1 as the smallest prime greater than or equal to P. The
// two differ exactly when P is prime (99 of the 477 K' values, the first
// being K' = 49). xssnick's encoder and decoder share the deviation, so it
// round-trips with itself, but its repair symbols for these K' are not those
// of RFC 6330 (graptorq matches cberner/raptorq there; see
// TestCbernerAllKPrime).
func xssnickP1Bug(p *rfc.Params) bool { return p.P1 == p.P }

// For every K' of RFC 6330 Table 2, encoding the same block with xssnick and
// graptorq must give identical source and repair symbols, except for the
// repair symbols of the K' affected by xssnick's P1 deviation, which must
// differ. Every other case uses the smallest K mapping to K' (so K != K' and
// the ESI-to-ISI offset is exercised), and the block length leaves a partial
// last symbol.
func TestXssnickEncoderDiff(t *testing.T) {
	sizes := []int{1, 3, 7, 13, 20, 1281}
	all := rfc.All()
	rng := rand.New(rand.NewPCG(21, 22))
	for i := range all {
		p := &all[i]
		if testing.Short() && i%10 != 0 {
			continue
		}
		K := p.KPrime
		if i%2 == 1 {
			K = all[i-1].KPrime + 1
		}
		T := sizes[i%len(sizes)]
		if K > 5000 && T > 20 {
			T = 20
		}
		data := testutil.PatternData(K*T-T/2, uint64(i))

		xe, err := xraptorq.NewRaptorQ(uint32(T)).CreateEncoder(data)
		if err != nil {
			t.Fatal(err)
		}
		ge, err := graptorq.NewBlockEncoder(data, T)
		if err != nil {
			t.Fatal(err)
		}
		if int(xe.BaseSymbolsNum()) != ge.K() || ge.K() != K {
			t.Fatalf("K'=%d: K mismatch: xssnick %d, graptorq %d, want %d", p.KPrime, xe.BaseSymbolsNum(), ge.K(), K)
		}
		esis := []uint32{0, uint32(K - 1), uint32(K), uint32(K + 1), uint32(K + 100), graptorq.MaxESI}
		for range 4 {
			esis = append(esis, uint32(rng.IntN(graptorq.MaxESI+1)))
		}
		deviant := xssnickP1Bug(p)
		repairDiffers := false
		for _, esi := range esis {
			want := xe.GenSymbol(esi)
			got, err := ge.AppendSymbol(nil, esi)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case bytes.Equal(got, want):
			case deviant && int(esi) >= K:
				repairDiffers = true
			default:
				t.Fatalf("K'=%d K=%d T=%d ESI=%d: graptorq %x, xssnick %x", p.KPrime, K, T, esi, got, want)
			}
		}
		if deviant && !repairDiffers {
			t.Errorf("K'=%d: xssnick no longer shows its P1 deviation; revisit xssnickP1Bug", p.KPrime)
		}
	}
}

// Symbols from one implementation must decode with the other, in both
// directions. Outputs are only compared when both sides succeed: xssnick's
// decoder adds fewer padding rows than RFC 6330, so it may need more symbols.
func TestXssnickCrossDecode(t *testing.T) {
	rng := rand.New(rand.NewPCG(23, 24))
	trials := 60
	if testing.Short() {
		trials = 15
	}
	for trial := range trials {
		T := 1 + rng.IntN(200)
		K := 1 + rng.IntN(2000)
		if p, _ := rfc.ForK(K); xssnickP1Bug(p) {
			continue // symbols are not RFC 6330 symbols for this K'
		}
		data := testutil.PatternData(K*T-rng.IntN(T), uint64(trial))
		loss := rng.Float64() * 0.5

		// xssnick encodes, graptorq decodes.
		xe, _ := xraptorq.NewRaptorQ(uint32(T)).CreateEncoder(data)
		gd, _ := graptorq.NewBlockDecoder(len(data), T)
		esi := uint32(0)
		for ; esi < uint32(K); esi++ {
			if rng.Float64() >= loss {
				gd.AddSymbol(esi, xe.GenSymbol(esi))
			}
		}
		for gd.Received() < K+2 {
			gd.AddSymbol(esi, xe.GenSymbol(esi))
			esi++
		}
		if err := gd.Decode(); err != nil {
			t.Fatalf("trial %d: graptorq failed on xssnick symbols (K=%d, T=%d): %v", trial, K, T, err)
		}
		if got, _ := gd.AppendSource(nil); !bytes.Equal(got, data) {
			t.Fatalf("trial %d: graptorq decoded xssnick symbols incorrectly", trial)
		}

		// graptorq encodes, xssnick decodes.
		ge, _ := graptorq.NewBlockEncoder(data, T)
		xd, err := xraptorq.NewRaptorQ(uint32(T)).CreateDecoder(uint32(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		received := 0
		for esi = 0; esi < uint32(K); esi++ {
			if rng.Float64() >= loss {
				sym, _ := ge.AppendSymbol(nil, esi)
				xd.AddSymbol(esi, sym)
				received++
			}
		}
		for ; ; esi++ {
			sym, _ := ge.AppendSymbol(nil, esi)
			if ready, _ := xd.AddSymbol(esi, sym); ready {
				received++
				if ok, got, err := xd.Decode(); err == nil && ok {
					if !bytes.Equal(got, data) {
						t.Fatalf("trial %d: xssnick decoded graptorq symbols incorrectly", trial)
					}
					break
				}
			}
			if received > K+50 {
				t.Fatalf("trial %d: xssnick could not decode graptorq symbols (K=%d)", trial, K)
			}
		}
	}
}

func BenchmarkCmp(b *testing.B) {
	for _, c := range []struct{ K, T int }{{100, 1280}, {1000, 1280}, {10000, 1280}, {50000, 256}} {
		data := testutil.PatternData(c.K*c.T, 1)
		name := fmt.Sprintf("K=%d/T=%d", c.K, c.T)

		for _, v := range []struct {
			lib string
			n   int
		}{{"graptorq", 1}, {"graptorq-par4", 4}} {
			b.Run("lib="+v.lib+"/op=encode/"+name, func(b *testing.B) {
				b.SetBytes(int64(len(data)))
				for b.Loop() {
					e, _ := graptorq.NewBlockEncoder(data, c.T, graptorq.WithConcurrency(v.n))
					if _, err := e.AppendSymbol(nil, uint32(c.K)); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
		b.Run("lib=xssnick/op=encode/"+name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				e, _ := xraptorq.NewRaptorQ(uint32(c.T)).CreateEncoder(data)
				e.GenSymbol(uint32(c.K))
			}
		})

		// Decode with 10% of the source symbols lost and replaced by repair
		// symbols, plus two extra. Both libraries deliver the decoded block
		// into a reused buffer (AppendSource, DecodeInto).
		ge, _ := graptorq.NewBlockEncoder(data, c.T)
		type sym struct {
			esi  uint32
			data []byte
		}
		var syms []sym
		lost := 0
		for esi := range uint32(c.K) {
			if esi%10 == 3 {
				lost++
				continue
			}
			s, _ := ge.AppendSymbol(nil, esi)
			syms = append(syms, sym{esi, s})
		}
		for i := range lost + 2 {
			esi := uint32(c.K + i)
			s, _ := ge.AppendSymbol(nil, esi)
			syms = append(syms, sym{esi, s})
		}
		for _, v := range []struct {
			lib string
			n   int
		}{{"graptorq", 1}, {"graptorq-par4", 4}} {
			b.Run("lib="+v.lib+"/op=decode/"+name, func(b *testing.B) {
				b.SetBytes(int64(len(data)))
				d, _ := graptorq.NewBlockDecoder(len(data), c.T, graptorq.WithConcurrency(v.n))
				out := make([]byte, 0, len(data))
				for b.Loop() {
					d.Reset()
					for _, s := range syms {
						d.AddSymbol(s.esi, s.data)
					}
					if err := d.Decode(); err != nil {
						b.Fatal(err)
					}
					out, _ = d.AppendSource(out[:0])
				}
			})
		}
		b.Run("lib=xssnick/op=decode/"+name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			d, _ := xraptorq.NewRaptorQ(uint32(c.T)).CreateDecoder(uint32(len(data)))
			out := make([]byte, len(data))
			for b.Loop() {
				d.Reset()
				for _, s := range syms {
					d.AddSymbol(s.esi, s.data)
				}
				if ok, err := d.DecodeInto(out); !ok || err != nil {
					b.Fatal("xssnick decode failed", err)
				}
			}
		})
	}
}
