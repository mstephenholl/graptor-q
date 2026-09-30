package graptorq

import (
	"bytes"
	"context"
	"errors"
	"math/rand/v2"
	"testing"

	"github.com/mholland/graptorq/internal/rfc"
	"github.com/mholland/graptorq/internal/solver"
)

func TestAppendSymbols(t *testing.T) {
	rng := rand.New(rand.NewPCG(101, 102))
	data := randomData(rng, 100*37-11) // K = 100, T = 37, partial last symbol
	enc, _ := NewBlockEncoder(data, 37)
	K := uint32(enc.K())
	for _, c := range []struct {
		first uint32
		n     int
	}{{0, 1}, {0, int(K)}, {K - 3, 6}, {K, 10}, {5000, 7}, {MaxESI - 2, 3}} {
		prefix := []byte("prefix")
		got, err := enc.AppendSymbols(bytes.Clone(prefix), c.first, c.n)
		if err != nil {
			t.Fatalf("AppendSymbols(%d, %d): %v", c.first, c.n, err)
		}
		want := bytes.Clone(prefix)
		for i := range c.n {
			want, _ = enc.AppendSymbol(want, c.first+uint32(i))
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("AppendSymbols(%d, %d) differs from AppendSymbol", c.first, c.n)
		}
	}
	dst := []byte("unchanged")
	if got, err := enc.AppendSymbols(dst, MaxESI-1, 3); !errors.Is(err, ErrESIRange) || !bytes.Equal(got, dst) {
		t.Errorf("past MaxESI: %q, %v", got, err)
	}
	if got, err := enc.AppendSymbols(dst, 0, 0); err != nil || !bytes.Equal(got, dst) {
		t.Errorf("n = 0: %q, %v", got, err)
	}
	if _, err := enc.AppendSymbols(dst, 0, -1); !errors.Is(err, ErrInvalidParameters) {
		t.Errorf("n < 0: %v", err)
	}
}

func TestAppendRepair(t *testing.T) {
	rng := rand.New(rand.NewPCG(103, 104))
	enc, _ := NewBlockEncoder(randomData(rng, 5000), 50)
	K := uint32(enc.K())
	got, err := enc.AppendRepair(nil, 7, 5)
	want, _ := enc.AppendSymbols(nil, K+7, 5)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("AppendRepair(7, 5) differs from AppendSymbols(K+7, 5): %v", err)
	}
	if _, err := enc.AppendRepair(nil, MaxESI-K+1, 1); !errors.Is(err, ErrESIRange) {
		t.Fatalf("repair past MaxESI: %v", err)
	}
	if _, err := enc.AppendRepair(nil, MaxESI-K-1, 3); !errors.Is(err, ErrESIRange) {
		t.Fatalf("repair range past MaxESI: %v", err)
	}
}

func TestAppendSymbolsNoAlloc(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are not meaningful with the race detector")
	}
	enc, _ := NewBlockEncoder(make([]byte, 64*1000), 64)
	buf := make([]byte, 0, 64*16)
	if _, err := enc.AppendRepair(buf, 0, 16); err != nil { // prepares
		t.Fatal(err)
	}
	var err error
	if n := testing.AllocsPerRun(100, func() { _, err = enc.AppendSymbols(buf[:0], 990, 16) }); n != 0 || err != nil {
		t.Errorf("AppendSymbols allocates %.0f times (err %v)", n, err)
	}
}

// Packets carrying four consecutive symbols each, with sub-blocks, some
// packets lost, must decode.
func TestMultiSymbolPackets(t *testing.T) {
	rng := rand.New(rand.NewPCG(105, 106))
	oti := OTI{TransferLength: 40000, SymbolSize: 48, SourceBlocks: 2, SubBlocks: 3, Alignment: 4}
	data := randomData(rng, oti.TransferLength)
	enc, _ := NewEncoder(data, oti)
	dec, _ := NewDecoder(oti)
	l := enc.Layout()
	const per = 4
	for sbn := range l.SourceBlocks() {
		K := uint32(l.Block(uint8(sbn)).K)
		for esi := uint32(0); !dec.Decoded(); esi += per {
			if esi < K && rng.IntN(5) == 0 {
				continue // lost
			}
			id := PayloadID{uint8(sbn), esi}
			pkt, _ := id.AppendBinary(nil)
			pkt, err := enc.AppendSymbols(pkt, id, per)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := dec.AddPacket(pkt); err != nil {
				t.Fatal(err)
			}
			if b, _ := dec.Block(uint8(sbn)); b.Decoded() {
				break
			}
		}
	}
	if got, err := dec.AppendObject(nil); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("multi-symbol packets decoded incorrectly (%v)", err)
	}
}

func TestMaxOverhead(t *testing.T) {
	rng := rand.New(rand.NewPCG(107, 108))
	data := randomData(rng, 200*32)
	enc, _ := NewBlockEncoder(data, 32)
	K := enc.K()

	// Deferred decoding: symbols are stored until Decode, so the cap is
	// what bounds memory.
	dec, _ := NewBlockDecoder(len(data), 32, WithMaxOverhead(3))
	for esi := range uint32(K + 50) {
		sym, _ := enc.AppendSymbol(nil, esi*3) // every third ESI: some repair
		added, err := dec.AddSymbol(esi*3, sym)
		if err != nil || added != (int(esi) < K+3) {
			t.Fatalf("symbol %d: added %v, err %v", esi, added, err)
		}
	}
	if dec.Received() != K+3 {
		t.Fatalf("stored %d symbols, want K+3 = %d", dec.Received(), K+3)
	}
	if err := dec.Decode(); err != nil {
		t.Fatal(err)
	}
	if got, _ := dec.AppendSource(nil); !bytes.Equal(got, data) {
		t.Fatal("wrong data")
	}

	// No limit (the default, or a negative n).
	for _, opts := range [][]Option{nil, {WithMaxOverhead(-1)}} {
		dec, _ := NewBlockDecoder(len(data), 32, opts...)
		for esi := uint32(K); dec.Received() < K+20; esi++ {
			sym, _ := enc.AppendSymbol(nil, esi)
			if added, _ := dec.AddSymbol(esi, sym); !added {
				t.Fatal("symbol ignored without a limit")
			}
		}
	}
}

// The documented limitation: with n = 0, an undecodable set of exactly K
// symbols leaves the block stuck, whatever arrives next, until Reset.
func TestMaxOverheadStuckUntilReset(t *testing.T) {
	rng := rand.New(rand.NewPCG(109, 110))
	const K, T = 10, 16
	p, _ := rfc.ForK(K)
	// Find K ESIs whose constraint matrix is singular (about 1% of sets).
	var esis []uint32
	for {
		esis = esis[:0]
		seen := map[uint32]bool{}
		for len(esis) < K {
			if x := uint32(rng.IntN(1000)); !seen[x] {
				seen[x] = true
				esis = append(esis, x)
			}
		}
		isis := []uint32{}
		for _, x := range esis {
			if x < K {
				isis = append(isis, x)
			} else {
				isis = append(isis, x+uint32(p.KPrime-K))
			}
		}
		if !solver.Solvable(p, isis) {
			break
		}
	}
	data := randomData(rng, K*T)
	enc, _ := NewBlockEncoder(data, T)
	dec, _ := NewBlockDecoder(K*T, T, WithMaxOverhead(0))
	for _, x := range esis {
		sendSymbol(t, enc, dec, x)
	}
	var de *DecodeError
	if err := dec.Decode(); !errors.As(err, &de) || de.Received != K {
		t.Fatalf("singular set decoded: %v", err)
	}
	sym, _ := enc.AppendSymbol(nil, 5000)
	if added, _ := dec.AddSymbol(5000, sym); added {
		t.Fatal("symbol beyond the cap was stored")
	}
	if err := dec.Decode(); !errors.Is(err, ErrInsufficientSymbols) {
		t.Fatalf("stuck block decoded: %v", err)
	}
	dec.Reset()
	for esi := uint32(2000); dec.Decode() != nil; esi++ {
		sendSymbol(t, enc, dec, esi)
		if esi > 2100 {
			t.Fatal("no decode after Reset")
		}
	}
	if got, _ := dec.AppendSource(nil); !bytes.Equal(got, data) {
		t.Fatal("wrong data after Reset")
	}
}

// The option reaches the source blocks of an object decoder.
func TestMaxOverheadObject(t *testing.T) {
	rng := rand.New(rand.NewPCG(111, 112))
	oti := OTI{TransferLength: 30000, SymbolSize: 64, SourceBlocks: 3, SubBlocks: 1, Alignment: 4}
	data := randomData(rng, oti.TransferLength)
	enc, _ := NewEncoder(data, oti)
	dec, _ := NewDecoder(oti, WithMaxOverhead(2))
	dec.SetDeferredDecode(true)
	for id, sym := range enc.Packets(30) {
		if _, err := dec.AddSymbol(id, sym); err != nil {
			t.Fatal(err)
		}
	}
	for sbn := range 3 {
		b, _ := dec.Block(uint8(sbn))
		if b.Received() != b.K()+2 {
			t.Fatalf("block %d stored %d symbols, want K+2", sbn, b.Received())
		}
	}
	if err := dec.Decode(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := dec.AppendObject(nil); !bytes.Equal(got, data) {
		t.Fatal("wrong data")
	}
}
