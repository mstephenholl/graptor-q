//go:build !goexperiment.simd

package interop

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"testing"

	rg "github.com/fgn/raptorgo"
	"github.com/mstephenholl/graptor-q"
	"github.com/mstephenholl/graptor-q/internal/testutil"
)

// rgOTI converts an OTI to raptorgo's.
func rgOTI(o graptorq.OTI) rg.OTI {
	return rg.OTI{
		TransferLength:  o.TransferLength,
		SymbolSize:      o.SymbolSize,
		SourceBlocks:    o.SourceBlocks,
		SubBlocks:       o.SubBlocks,
		SymbolAlignment: o.Alignment,
	}
}

// The parameter derivation of RFC 6330 Section 4.3 must agree with raptorgo's
// for every input: the same OTI, or both rejecting the input.
func TestRaptorgoDeriveOTI(t *testing.T) {
	rng := rand.New(rand.NewPCG(41, 42))
	transfer := []uint64{1, 100, 4097, 1 << 20, 123_456_789, 5 << 30, 1 << 36, rg.MaxTransferLength}
	payload := []int{8, 64, 1024, 1280, 1400, 65528}
	for range 40 {
		transfer = append(transfer, 1+rng.Uint64N(1<<34))
		payload = append(payload, 8*(1+rng.IntN(8191)))
	}
	cases := 0
	for _, F := range transfer {
		for _, P := range payload {
			for _, Al := range []int{1, 4, 8} {
				for _, SS := range []int{1, 8} {
					for _, WS := range []int64{64 << 10, 1 << 20, 16 << 20, 1 << 30} {
						if P%Al != 0 || SS*Al > P {
							continue
						}
						cases++
						want, rerr := rg.DeriveOTI(rg.DerivationInput{
							TransferLength:             F,
							DecoderBlockBytes:          uint64(WS),
							MaxPayloadSize:             uint16(P),
							SymbolAlignment:            uint8(Al),
							MinSubSymbolAlignmentUnits: uint32(SS),
						}, rg.RFCWireLimits())
						got, gerr := graptorq.DeriveOTI(F, graptorq.Config{
							PayloadSize:      P,
							Alignment:        Al,
							MinSubSymbolSize: SS,
							WorkingMemory:    WS,
						})
						in := fmt.Sprintf("F=%d P'=%d Al=%d SS=%d WS=%d", F, P, Al, SS, WS)
						switch {
						case rerr != nil && gerr != nil:
						case rerr != nil || gerr != nil:
							t.Errorf("%s: graptorq %+v (%v), raptorgo %+v (%v)", in, got, gerr, want, rerr)
						case rgOTI(got) != want:
							t.Errorf("%s: graptorq %+v, raptorgo %+v", in, got, want)
						}
					}
				}
			}
		}
	}
	t.Logf("%d derivations compared", cases)
}

// testOTIs returns objects with several source blocks and sub-blocks,
// alignments from 1 to 8 and partial last symbols, plus random valid ones.
func testOTIs(rng *rand.Rand, n int) []graptorq.OTI {
	otis := []graptorq.OTI{
		{TransferLength: 1, SymbolSize: 4, SourceBlocks: 1, SubBlocks: 1, Alignment: 4},
		{TransferLength: 10_000, SymbolSize: 64, SourceBlocks: 1, SubBlocks: 1, Alignment: 4},
		{TransferLength: 100_003, SymbolSize: 128, SourceBlocks: 3, SubBlocks: 1, Alignment: 4},
		{TransferLength: 40_000, SymbolSize: 48, SourceBlocks: 2, SubBlocks: 3, Alignment: 4},
		{TransferLength: 250_001, SymbolSize: 1024, SourceBlocks: 5, SubBlocks: 7, Alignment: 8},
		{TransferLength: 77_777, SymbolSize: 30, SourceBlocks: 4, SubBlocks: 5, Alignment: 2},
		{TransferLength: 12_345, SymbolSize: 13, SourceBlocks: 2, SubBlocks: 13, Alignment: 1},
	}
	for len(otis) < n {
		Al := []int{1, 2, 4, 8}[rng.IntN(4)]
		T := Al * (1 + rng.IntN(64))
		F := 1 + rng.IntN(200_000)
		Kt := (F + T - 1) / T
		oti := graptorq.OTI{
			TransferLength: uint64(F),
			SymbolSize:     uint16(T),
			SourceBlocks:   uint8(1 + rng.IntN(min(4, Kt))),
			SubBlocks:      uint16(1 + rng.IntN(min(8, T/Al))),
			Alignment:      uint8(Al),
		}
		if oti.Validate() == nil {
			otis = append(otis, oti)
		}
	}
	return otis
}

// raptorgo and graptorq must produce byte-identical packets for the same
// object, source and repair, single symbols and groups, with and without the
// padding at the end of source symbols (raptorgo's shortenFinal, graptorq's
// WithTrimmedPadding). With sub-blocks, several of the last source symbols
// can end with padding, so packets end at each of the last six.
func TestRaptorgoPacketDiff(t *testing.T) {
	rng := rand.New(rand.NewPCG(43, 44))
	trimmed := 0
	for i, oti := range testOTIs(rng, 20) {
		data := testutil.PatternData(int(oti.TransferLength), uint64(i))
		re, err := rg.NewObjectEncoder(data, rgOTI(oti), rg.RFCWireLimits())
		if err != nil {
			t.Fatal(err)
		}
		for _, trim := range []bool{false, true} {
			var opts []graptorq.Option
			if trim {
				opts = append(opts, graptorq.WithTrimmedPadding())
			}
			ge, err := graptorq.NewEncoder(data, oti, opts...)
			if err != nil {
				t.Fatal(err)
			}
			l := ge.Layout()
			for sbn := range l.SourceBlocks() {
				K := uint32(l.Block(uint8(sbn)).K)
				g := min(3, K)
				cases := []struct{ esi, n uint32 }{
					{0, 1}, {0, g}, // source
					{K, 1}, {K + 5, 4}, {graptorq.MaxESI, 1}, {graptorq.MaxESI - 3, 4}, // repair
				}
				for j := uint32(1); j <= min(6, K); j++ { // ending at source symbol K-j
					cases = append(cases, struct{ esi, n uint32 }{K - j, 1})
					cases = append(cases, struct{ esi, n uint32 }{K - j - min(2, K-j), min(2, K-j) + 1})
				}
				for _, c := range cases {
					want, err := re.Packet(uint8(sbn), c.esi, c.n, trim)
					if err != nil {
						t.Fatal(err)
					}
					id := graptorq.PayloadID{SBN: uint8(sbn), ESI: c.esi}
					got, _ := id.AppendBinary(nil)
					if got, err = ge.AppendSymbols(got, id, int(c.n)); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, want) {
						t.Fatalf("%+v trim=%v SBN=%d ESI=%d n=%d: packets differ (%d and %d bytes)", oti, trim, sbn, c.esi, c.n, len(got), len(want))
					}
					if len(got) < graptorq.PayloadIDSize+int(c.n)*l.SymbolSize {
						trimmed++
					}
				}
			}
		}
	}
	if trimmed == 0 {
		t.Fatal("no packet was trimmed")
	}
	t.Logf("%d trimmed packets", trimmed)
}

// Objects encoded by one implementation must decode with the other, in both
// directions: packets of one to four symbols, source packets lost at random,
// then repair packets until the block decodes. Odd trials leave out the
// padding at the end of source symbols.
func TestRaptorgoCrossDecode(t *testing.T) {
	rng := rand.New(rand.NewPCG(45, 46))
	n := 30
	if testing.Short() {
		n = 10
	}
	for i, oti := range testOTIs(rng, n) {
		data := testutil.PatternData(int(oti.TransferLength), uint64(i))
		loss := rng.Float64() * 0.5
		l, err := oti.Layout()
		if err != nil {
			t.Fatal(err)
		}
		// send sends the packets of every source block through add until
		// decoded reports the block decoded.
		send := func(pkt func(sbn uint8, esi, n uint32) []byte, add func([]byte), decoded func(sbn uint8) bool) {
			for sbn := range uint8(l.SourceBlocks()) {
				K := uint32(l.Block(sbn).K)
				for esi := uint32(0); esi < K; {
					n := min(1+uint32(rng.IntN(4)), K-esi)
					if rng.Float64() >= loss {
						add(pkt(sbn, esi, n))
					}
					esi += n
				}
				for esi := K; !decoded(sbn); esi += 2 {
					if esi > 2*K+100 {
						t.Fatalf("%+v: block %d does not decode", oti, sbn)
					}
					add(pkt(sbn, esi, 2))
				}
			}
		}

		// Odd trials leave out the padding at the end of source symbols.
		trim := i%2 == 1
		var opts []graptorq.Option
		if trim {
			opts = append(opts, graptorq.WithTrimmedPadding())
		}

		// graptorq encodes, raptorgo decodes.
		ge, err := graptorq.NewEncoder(data, oti, opts...)
		if err != nil {
			t.Fatal(err)
		}
		var got []byte
		verify := func(r rg.VerificationRequest) error {
			b, err := io.ReadAll(r.Object)
			if err != nil || !bytes.Equal(b, data) {
				return errors.New("decoded object differs")
			}
			return nil
		}
		deliver := func(v rg.VerifiedObject) error { got = v.Bytes; return nil }
		rd, err := rg.NewObjectDecoder(rgOTI(oti), rg.DefaultLimits(), "interop", verify, deliver)
		if err != nil {
			t.Fatal(err)
		}
		completed := uint32(0)
		send(func(sbn uint8, esi, n uint32) []byte {
			id := graptorq.PayloadID{SBN: sbn, ESI: esi}
			pkt, _ := id.AppendBinary(nil)
			pkt, err := ge.AppendSymbols(pkt, id, int(n))
			if err != nil {
				t.Fatal(err)
			}
			return pkt
		}, func(pkt []byte) {
			if _, err := rd.AddPacket(pkt); err != nil {
				t.Fatalf("%+v: raptorgo: %v", oti, err)
			}
		}, func(uint8) bool {
			if c := rd.CompletedBlocks(); c > completed {
				completed = c
				return true
			}
			return false
		})
		if s := rd.State(); s != rg.ObjectDelivered || !bytes.Equal(got, data) {
			t.Fatalf("%+v: raptorgo decoder state %v after graptorq packets", oti, s)
		}

		// raptorgo encodes, graptorq decodes.
		re, err := rg.NewObjectEncoder(data, rgOTI(oti), rg.RFCWireLimits())
		if err != nil {
			t.Fatal(err)
		}
		gd, err := graptorq.NewDecoder(oti)
		if err != nil {
			t.Fatal(err)
		}
		send(func(sbn uint8, esi, n uint32) []byte {
			pkt, err := re.Packet(sbn, esi, n, trim)
			if err != nil {
				t.Fatal(err)
			}
			return pkt
		}, func(pkt []byte) {
			if _, err := gd.AddPacket(pkt); err != nil {
				t.Fatalf("%+v: graptorq: %v", oti, err)
			}
		}, func(sbn uint8) bool {
			b, err := gd.Block(sbn)
			return err == nil && b.Decoded()
		})
		if got, err := gd.AppendObject(nil); err != nil || !bytes.Equal(got, data) {
			t.Fatalf("%+v: graptorq decoded raptorgo packets incorrectly (%v)", oti, err)
		}
	}
}

// Asked to, raptorgo leaves out the padding at the end of every source symbol
// that ends with padding (RFC 6330 Section 4.4.2): with sub-blocks, several
// symbols of the last source block can. graptorq must restore it. Other source
// packets are lost at random, so decoding needs the restored symbols in the
// solve.
func TestRaptorgoShortenedPackets(t *testing.T) {
	rng := rand.New(rand.NewPCG(47, 48))
	shortened := 0
	for i, oti := range testOTIs(rng, 20) {
		data := testutil.PatternData(int(oti.TransferLength), uint64(i))
		re, err := rg.NewObjectEncoder(data, rgOTI(oti), rg.RFCWireLimits())
		if err != nil {
			t.Fatal(err)
		}
		gd, err := graptorq.NewDecoder(oti)
		if err != nil {
			t.Fatal(err)
		}
		l, err := oti.Layout()
		if err != nil {
			t.Fatal(err)
		}
		// send sends a packet, except that whole source packets are lost
		// with probability lose.
		send := func(sbn uint8, esi uint32, lose float64) {
			pkt, err := re.Packet(sbn, esi, 1, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(pkt) < graptorq.PayloadIDSize+l.SymbolSize {
				shortened++
			} else if rng.Float64() < lose {
				return
			}
			if _, err := gd.AddPacket(pkt); err != nil {
				t.Fatalf("%+v SBN=%d ESI=%d: %v", oti, sbn, esi, err)
			}
		}
		for sbn := range uint8(l.SourceBlocks()) {
			K := uint32(l.Block(sbn).K)
			for esi := range K {
				send(sbn, esi, 0.2)
			}
			for esi := K; ; esi++ {
				if b, _ := gd.Block(sbn); b.Decoded() {
					break
				}
				send(sbn, esi, 0)
			}
		}
		if got, err := gd.AppendObject(nil); err != nil || !bytes.Equal(got, data) {
			t.Fatalf("%+v: graptorq decoded shortened raptorgo packets incorrectly (%v)", oti, err)
		}
	}
	if shortened == 0 {
		t.Fatal("raptorgo shortened no packet")
	}
	t.Logf("%d shortened packets", shortened)
}

// benchRaptorgo adds raptorgo's object benchmarks to BenchmarkCmp: pkts are
// the single-symbol packets of the decode benchmark.
func benchRaptorgo(b *testing.B, name string, K int, oti graptorq.OTI, data []byte, pkts [][]byte) {
	b.Run("lib=raptorgo/op=object-encode/"+name, func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		for b.Loop() {
			e, err := rg.NewObjectEncoder(data, rgOTI(oti), rg.DefaultLimits())
			if err != nil {
				b.Fatal(err)
			}
			if _, err := e.Packet(0, uint32(K), 1, false); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("lib=raptorgo/op=object-decode/"+name, func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		verify := func(rg.VerificationRequest) error { return nil }
		delivered := false
		deliver := func(rg.VerifiedObject) error { delivered = true; return nil }
		for b.Loop() {
			delivered = false
			d, err := rg.NewObjectDecoder(rgOTI(oti), rg.DefaultLimits(), "bench", verify, deliver)
			if err != nil {
				b.Fatal(err)
			}
			for _, p := range pkts {
				if d.State() != rg.ObjectReceiving {
					break
				}
				if _, err := d.AddPacket(p); err != nil {
					b.Fatal(err)
				}
			}
			if !delivered {
				b.Fatalf("raptorgo: object not delivered (state %v)", d.State())
			}
		}
	})
}
