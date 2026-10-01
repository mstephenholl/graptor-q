package interop

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"testing"

	"github.com/mstephenholl/graptor-q"
	"github.com/mstephenholl/graptor-q/internal/testutil"
	tyh "github.com/takeyourhatoff/raptorq"
)

// For every K' of RFC 6330 Table 2 (see encoderCases), encoding the same
// block with takeyourhatoff/raptorq and graptorq must give identical source
// and repair symbols. Although it derives from xssnick/raptorq, it does not
// share xssnick's P1 deviation.
func TestTakeyourhatoffEncoderDiff(t *testing.T) {
	encoderCases(t, 31, func(c encoderCase) {
		te, err := tyh.NewEncoder(c.data, c.T)
		if err != nil {
			t.Fatal(err)
		}
		ge, err := graptorq.NewBlockEncoder(c.data, c.T)
		if err != nil {
			t.Fatal(err)
		}
		if int(te.SourceSymbols()) != c.K || ge.K() != c.K {
			t.Fatalf("K'=%d: K mismatch: takeyourhatoff %d, graptorq %d, want %d", c.p.KPrime, te.SourceSymbols(), ge.K(), c.K)
		}
		want := make([]byte, c.T)
		for _, esi := range c.esis {
			if err := te.Encode(want, esi); err != nil {
				t.Fatal(err)
			}
			got, err := ge.AppendSymbol(nil, esi)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("K'=%d K=%d T=%d ESI=%d: graptorq %x, takeyourhatoff %x", c.p.KPrime, c.K, c.T, esi, got, want)
			}
		}
	})
}

// Symbols from one implementation must decode with the other, in both
// directions: source symbols with random loss, then repair symbols until
// decoding succeeds.
func TestTakeyourhatoffCrossDecode(t *testing.T) {
	rng := rand.New(rand.NewPCG(33, 34))
	trials := 60
	if testing.Short() {
		trials = 15
	}
	for trial := range trials {
		T := 1 + rng.IntN(200)
		K := 1 + rng.IntN(2000)
		data := testutil.PatternData(K*T-rng.IntN(T), uint64(trial))
		loss := rng.Float64() * 0.5

		// takeyourhatoff encodes, graptorq decodes.
		te, err := tyh.NewEncoder(data, T)
		if err != nil {
			t.Fatal(err)
		}
		gd, err := graptorq.NewBlockDecoder(len(data), T)
		if err != nil {
			t.Fatal(err)
		}
		sym := make([]byte, T)
		send := func(esi uint32) {
			if err := te.Encode(sym, esi); err != nil {
				t.Fatal(err)
			}
			if _, err := gd.AddSymbol(esi, sym); err != nil {
				t.Fatal(err)
			}
		}
		for esi := range uint32(K) {
			if rng.Float64() >= loss {
				send(esi)
			}
		}
		esi := uint32(K)
		for ; gd.Received() < K; esi++ {
			send(esi)
		}
		for ; gd.Decode() != nil; esi++ {
			if gd.Received() > K+50 {
				t.Fatalf("trial %d: graptorq could not decode takeyourhatoff symbols (K=%d, T=%d)", trial, K, T)
			}
			send(esi)
		}
		if got, _ := gd.AppendSource(nil); !bytes.Equal(got, data) {
			t.Fatalf("trial %d: graptorq decoded takeyourhatoff symbols incorrectly", trial)
		}

		// graptorq encodes, takeyourhatoff decodes.
		ge, err := graptorq.NewBlockEncoder(data, T)
		if err != nil {
			t.Fatal(err)
		}
		td, err := tyh.NewDecoder(len(data), T)
		if err != nil {
			t.Fatal(err)
		}
		received := 0
		add := func(esi uint32) {
			s, err := ge.AppendSymbol(nil, esi)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := td.Add(esi, s); err != nil {
				t.Fatal(err)
			}
			received++
		}
		for esi := range uint32(K) {
			if rng.Float64() >= loss {
				add(esi)
			}
		}
		esi = uint32(K)
		for ; received < K; esi++ {
			add(esi)
		}
		out := make([]byte, len(data))
		for {
			err := td.Decode(out)
			if err == nil {
				break
			}
			if !errors.Is(err, tyh.ErrNotEnoughSymbols) {
				t.Fatalf("trial %d: takeyourhatoff: %v", trial, err)
			}
			if received > K+50 {
				t.Fatalf("trial %d: takeyourhatoff could not decode graptorq symbols (K=%d, T=%d)", trial, K, T)
			}
			add(esi)
			esi++
		}
		if !bytes.Equal(out, data) {
			t.Fatalf("trial %d: takeyourhatoff decoded graptorq symbols incorrectly", trial)
		}
	}
}
