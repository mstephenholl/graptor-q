package graptorq

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"math/rand/v2"
	"strconv"
	"testing"
)

func TestOTIEncoding(t *testing.T) {
	o := OTI{TransferLength: 0x0102030405, SymbolSize: 0x0a0b, SourceBlocks: 0xfe, SubBlocks: 0x0102, Alignment: 1}
	b, err := o.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(b); got != "0102030405000a0bfe010201" {
		t.Fatalf("encoding = %s", got)
	}
	b[5] = 0xff // reserved octet: ignored on receipt
	var back OTI
	if err := back.UnmarshalBinary(b); err != nil || back != o {
		t.Fatalf("round trip = %+v, %v", back, err)
	}
	if _, err := o.AppendBinary(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseOTI(b[:11]); !errors.Is(err, ErrInvalidOTI) {
		t.Errorf("short OTI: %v", err)
	}
}

func TestOTIValidate(t *testing.T) {
	ok := OTI{TransferLength: 1000, SymbolSize: 64, SourceBlocks: 2, SubBlocks: 4, Alignment: 8}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]OTI{
		"F=0":               {0, 64, 1, 1, 8},
		"F>max":             {MaxTransferLength + 1, 65528, 255, 1, 8},
		"T not multiple":    {1000, 63, 1, 1, 8},
		"Al=0":              {1000, 64, 1, 1, 0},
		"Z=0":               {1000, 64, 0, 1, 8},
		"Z>Kt":              {100, 64, 3, 1, 8},
		"N=0":               {1000, 64, 1, 0, 8},
		"N>T/Al":            {1000, 64, 1, 9, 8},
		"K>56403 per block": {56404 * 4, 4, 1, 1, 4},
	}
	for name, o := range bad {
		if err := o.Validate(); !errors.Is(err, ErrInvalidOTI) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	max := OTI{MaxTransferLength, 65535, 255, 1, 1}
	if err := max.Validate(); err != nil {
		t.Errorf("largest object: %v", err)
	}
}

func TestPayloadID(t *testing.T) {
	b, err := PayloadID{SBN: 7, ESI: 0xabcdef}.AppendBinary(nil)
	if err != nil || hex.EncodeToString(b) != "07abcdef" {
		t.Fatalf("%x %v", b, err)
	}
	if id, _ := ParsePayloadID(b); id != (PayloadID{7, 0xabcdef}) {
		t.Fatalf("parse = %+v", id)
	}
	if _, err := (PayloadID{ESI: MaxESI + 1}).AppendBinary(nil); !errors.Is(err, ErrESIRange) {
		t.Fatal(err)
	}
}

func TestPartition(t *testing.T) {
	for _, c := range [][6]int{
		{10, 3, 4, 3, 1, 2}, {9, 3, 3, 3, 0, 3}, {1, 1, 1, 1, 0, 1}, {100, 7, 15, 14, 2, 5},
	} {
		il, is, jl, js := Partition(c[0], c[1])
		if il != c[2] || is != c[3] || jl != c[4] || js != c[5] || il*jl+is*js != c[0] {
			t.Errorf("Partition(%d,%d) = %d %d %d %d", c[0], c[1], il, is, jl, js)
		}
	}
}

// Expected values computed by an independent Python implementation of
// Section 4.3 over Table 2 parsed from the RFC text.
func TestDeriveOTI(t *testing.T) {
	for _, c := range []struct {
		F         uint64
		P, Al, SS int
		WS        int64
		Z, N      int
	}{
		{1000000, 1024, 4, 1, 16 << 20, 1, 1},
		{10485760, 1024, 4, 8, 256 << 10, 2, 22},
		{123456789, 1280, 8, 8, 1 << 20, 6, 20},
		{5, 4, 4, 1, 16 << 20, 1, 1},
		{3000000000, 65532, 4, 2, 64 << 20, 1, 46},
		{700000, 1000, 4, 5, 300000, 1, 3},
	} {
		o, err := DeriveOTI(c.F, Config{PayloadSize: c.P, Alignment: c.Al, MinSubSymbolSize: c.SS, WorkingMemory: c.WS})
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		want := OTI{c.F, uint16(c.P), uint8(c.Z), uint16(c.N), uint8(c.Al)}
		if o != want {
			t.Errorf("DeriveOTI(%+v) = %+v, want %+v", c, o, want)
		}
	}
	if _, err := DeriveOTI(1<<30, Config{PayloadSize: 64, WorkingMemory: 1 << 10}); !errors.Is(err, ErrInvalidParameters) {
		t.Errorf("too many source blocks: %v", err)
	}
	if _, err := DeriveOTI(100, Config{PayloadSize: 10}); !errors.Is(err, ErrInvalidParameters) {
		t.Errorf("P' not a multiple of Al: %v", err)
	}
}

// Hand-worked interleaving: T=8, Al=2, N=3 gives sub-symbols of 4, 2 and 2
// bytes (Partition(4, 3) = (2, 1, 1, 2) in units of Al).
func TestGatherScatter(t *testing.T) {
	o := OTI{TransferLength: 16, SymbolSize: 8, SourceBlocks: 1, SubBlocks: 3, Alignment: 2}
	l, err := o.Layout()
	if err != nil {
		t.Fatal(err)
	}
	block := make([]byte, 16)
	for i := range block {
		block[i] = byte(i)
	}
	sym := make([]byte, 16)
	l.gather(sym, block, 2)
	want := []byte{0, 1, 2, 3, 8, 9, 12, 13, 4, 5, 6, 7, 10, 11, 14, 15}
	if !bytes.Equal(sym, want) {
		t.Fatalf("gather = %v, want %v", sym, want)
	}
	back := make([]byte, 16)
	l.scatter(back, sym, 2)
	if !bytes.Equal(back, block) {
		t.Fatalf("scatter = %v", back)
	}
	// A short block is zero padded.
	l.gather(sym, block[:11], 2)
	want = []byte{0, 1, 2, 3, 8, 9, 0, 0, 4, 5, 6, 7, 10, 0, 0, 0}
	if !bytes.Equal(sym, want) {
		t.Fatalf("gather short = %v, want %v", sym, want)
	}
}

func randomOTI(rng *rand.Rand) OTI {
	al := []int{1, 2, 4, 8}[rng.IntN(4)]
	T := al * (1 + rng.IntN(64/al+8))
	F := uint64(1 + rng.IntN(60000))
	kt := int(ceilDiv(F, uint64(T)))
	z := 1 + rng.IntN(min(kt, 6))
	n := 1 + rng.IntN(min(T/al, 5))
	return OTI{TransferLength: F, SymbolSize: uint16(T), SourceBlocks: uint8(z), SubBlocks: uint16(n), Alignment: uint8(al)}
}

func TestObjectRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 14))
	trials := 150
	if testing.Short() {
		trials = 40
	}
	for trial := range trials {
		oti := randomOTI(rng)
		data := make([]byte, oti.TransferLength)
		for i := range data {
			data[i] = byte(rng.Uint32())
		}
		enc, err := NewEncoder(data, oti)
		if err != nil {
			t.Fatalf("%+v: %v", oti, err)
		}
		dec, err := NewDecoder(oti)
		if err != nil {
			t.Fatal(err)
		}
		deferred := trial%3 == 0
		dec.SetDeferredDecode(deferred)
		l := enc.Layout()
		// Send each block's packets with 30% loss, then repair packets
		// (sometimes two symbols per packet) until everything decodes.
		loss := 0.3
		for sbn := range l.SourceBlocks() {
			info := l.Block(uint8(sbn))
			for esi := range uint32(info.K) {
				if rng.Float64() < loss {
					continue
				}
				pkt, err := enc.AppendPacket(nil, PayloadID{uint8(sbn), esi})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := dec.AddPacket(pkt); err != nil {
					t.Fatal(err)
				}
			}
		}
		next := make([]uint32, l.SourceBlocks())
		for sbn := range next {
			info := l.Block(uint8(sbn))
			next[sbn] = uint32(info.K)
			b, _ := dec.Block(uint8(sbn))
			for b.Received() < info.K && !dec.Decoded() {
				pkt, _ := enc.AppendPacket(nil, PayloadID{uint8(sbn), next[sbn]})
				next[sbn]++
				if _, err := dec.AddPacket(pkt); err != nil {
					t.Fatal(err)
				}
			}
		}
		for round := 0; ; round++ {
			if deferred {
				if err := dec.Decode(context.Background()); err == nil {
					break
				} else if !errors.Is(err, ErrInsufficientSymbols) {
					t.Fatal(err)
				}
			} else if dec.Decoded() {
				break
			}
			if round > 20 {
				t.Fatalf("trial %d %+v: not decoded", trial, oti)
			}
			for sbn := range next {
				id := PayloadID{uint8(sbn), next[sbn]}
				pkt, _ := enc.AppendPacket(nil, id)
				if round%2 == 1 {
					pkt, _ = enc.AppendSymbol(pkt, PayloadID{uint8(sbn), next[sbn] + 1})
					next[sbn]++
				}
				next[sbn]++
				if _, err := dec.AddPacket(pkt); err != nil {
					t.Fatal(err)
				}
			}
		}
		got, err := dec.AppendObject(nil)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("trial %d %+v: object mismatch (%v)", trial, oti, err)
		}
		var buf bytes.Buffer
		if n, err := dec.WriteTo(&buf); err != nil || n != int64(len(data)) || !bytes.Equal(buf.Bytes(), data) {
			t.Fatalf("WriteTo: %d %v", n, err)
		}
	}
}

// With N = 1 the object encoder is the block encoder applied to each block.
func TestEncoderMatchesBlockEncoder(t *testing.T) {
	data := make([]byte, 10000)
	for i := range data {
		data[i] = byte(i * 7)
	}
	oti := OTI{TransferLength: 10000, SymbolSize: 64, SourceBlocks: 3, SubBlocks: 1, Alignment: 4}
	enc, _ := NewEncoder(data, oti)
	if err := enc.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	l := enc.Layout()
	for sbn := range l.SourceBlocks() {
		info := l.Block(uint8(sbn))
		ref, _ := NewBlockEncoder(data[info.Offset:info.Offset+uint64(info.Length)], 64)
		for _, esi := range []uint32{0, uint32(info.K - 1), uint32(info.K), 12345} {
			a, _ := enc.AppendSymbol(nil, PayloadID{uint8(sbn), esi})
			b, _ := ref.AppendSymbol(nil, esi)
			if !bytes.Equal(a, b) {
				t.Fatalf("block %d ESI %d differs", sbn, esi)
			}
		}
	}
	count := 0
	for id, sym := range enc.Packets(2) {
		if len(sym) != 64 || int(id.SBN) >= l.SourceBlocks() {
			t.Fatal("bad packet")
		}
		count++
	}
	if count != l.Kt+2*l.SourceBlocks() {
		t.Fatalf("Packets yielded %d symbols, want %d", count, l.Kt+2*l.SourceBlocks())
	}
}

func TestDecoderErrors(t *testing.T) {
	oti := OTI{TransferLength: 1000, SymbolSize: 64, SourceBlocks: 2, SubBlocks: 1, Alignment: 4}
	dec, _ := NewDecoder(oti)
	if _, err := dec.AddPacket([]byte{0, 0}); err == nil {
		t.Error("short packet accepted")
	}
	if _, err := dec.AddPacket(append([]byte{5, 0, 0, 0}, make([]byte, 64)...)); !errors.Is(err, ErrSBNRange) {
		t.Errorf("bad SBN: %v", err)
	}
	if _, err := dec.AddPacket(append([]byte{0, 0, 0, 0}, make([]byte, 63)...)); !errors.Is(err, ErrSymbolSize) {
		t.Errorf("bad size: %v", err)
	}
	if _, err := dec.AppendObject(nil); !errors.Is(err, ErrNotDecoded) {
		t.Errorf("AppendObject: %v", err)
	}
	if err := dec.Decode(context.Background()); !errors.Is(err, ErrInsufficientSymbols) {
		t.Errorf("Decode: %v", err)
	}
	if _, err := NewEncoder(make([]byte, 999), oti); !errors.Is(err, ErrInvalidOTI) {
		t.Errorf("length mismatch: %v", err)
	}
}

// The largest blocks (K = 56403, T = 65535: about 3.7 GB of working memory)
// cannot be addressed on 32-bit platforms and must be rejected cleanly there;
// on 64-bit platforms the decoder accepts them without allocating up front.
func TestHugeBlockGuard(t *testing.T) {
	oti := OTI{TransferLength: 56403 * 65535, SymbolSize: 65535, SourceBlocks: 1, SubBlocks: 1, Alignment: 1}
	l, err := oti.Layout()
	if err != nil {
		t.Fatal(err)
	}
	if b := l.Block(0); b.Length != 56403*65535 {
		t.Fatalf("block length %d", b.Length)
	}
	dec, err := NewDecoder(oti)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dec.AddSymbol(PayloadID{ESI: 0}, make([]byte, 65535))
	if strconv.IntSize == 32 {
		if !errors.Is(err, ErrMemoryLimit) {
			t.Fatalf("32-bit: err = %v, want ErrMemoryLimit", err)
		}
	} else if err != nil {
		t.Fatalf("64-bit: %v", err)
	}
}
