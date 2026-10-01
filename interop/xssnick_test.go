package interop

import (
	"bytes"
	"math/rand/v2"
	"testing"

	"github.com/mstephenholl/graptor-q"
	"github.com/mstephenholl/graptor-q/internal/rfc"
	"github.com/mstephenholl/graptor-q/internal/testutil"
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

// For every K' of RFC 6330 Table 2 (see encoderCases), encoding the same
// block with xssnick and graptorq must give identical source and repair
// symbols, except for the repair symbols of the K' affected by xssnick's P1
// deviation, which must differ.
func TestXssnickEncoderDiff(t *testing.T) {
	encoderCases(t, 21, func(c encoderCase) {
		xe, err := xraptorq.NewRaptorQ(uint32(c.T)).CreateEncoder(c.data)
		if err != nil {
			t.Fatal(err)
		}
		ge, err := graptorq.NewBlockEncoder(c.data, c.T)
		if err != nil {
			t.Fatal(err)
		}
		if int(xe.BaseSymbolsNum()) != ge.K() || ge.K() != c.K {
			t.Fatalf("K'=%d: K mismatch: xssnick %d, graptorq %d, want %d", c.p.KPrime, xe.BaseSymbolsNum(), ge.K(), c.K)
		}
		deviant := xssnickP1Bug(c.p)
		repairDiffers := false
		for _, esi := range c.esis {
			want := xe.GenSymbol(esi)
			got, err := ge.AppendSymbol(nil, esi)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case bytes.Equal(got, want):
			case deviant && int(esi) >= c.K:
				repairDiffers = true
			default:
				t.Fatalf("K'=%d K=%d T=%d ESI=%d: graptorq %x, xssnick %x", c.p.KPrime, c.K, c.T, esi, got, want)
			}
		}
		if deviant && !repairDiffers {
			t.Errorf("K'=%d: xssnick no longer shows its P1 deviation; revisit xssnickP1Bug", c.p.KPrime)
		}
	})
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
		for esi := range uint32(K) {
			if rng.Float64() >= loss {
				if _, err := gd.AddSymbol(esi, xe.GenSymbol(esi)); err != nil {
					t.Fatal(err)
				}
			}
		}
		for esi := uint32(K); gd.Received() < K+2; esi++ {
			if _, err := gd.AddSymbol(esi, xe.GenSymbol(esi)); err != nil {
				t.Fatal(err)
			}
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
		for esi := range uint32(K) {
			if rng.Float64() >= loss {
				sym, _ := ge.AppendSymbol(nil, esi)
				if _, err := xd.AddSymbol(esi, sym); err != nil {
					t.Fatal(err)
				}
				received++
			}
		}
		for esi := uint32(K); ; esi++ {
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
