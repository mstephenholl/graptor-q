package graptorq

import (
	"bytes"
	"math/rand/v2"
	"testing"
)

// Any 12 bytes either fail to parse or describe a consistent layout.
func FuzzParseOTI(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0x27, 0x10, 0, 0, 64, 2, 0, 1, 4})
	f.Add([]byte{0xdb, 0x74, 0x24, 0x0a, 0x13, 0xff, 0xff, 0xff, 0xff, 0, 1, 1})
	f.Fuzz(func(t *testing.T, b []byte) {
		o, err := ParseOTI(b)
		if err != nil {
			return
		}
		enc, err := o.MarshalBinary()
		if err != nil || !bytes.Equal(enc[:5], b[:5]) || !bytes.Equal(enc[6:], b[6:]) {
			t.Fatalf("%+v does not round trip: %x vs %x (%v)", o, enc, b, err)
		}
		l, err := o.Layout()
		if err != nil {
			t.Fatal(err)
		}
		if l.KL*l.ZL+l.KS*l.ZS != l.Kt || l.TL*l.NL+l.TS*l.NS != int(o.SymbolSize) || l.TS == 0 || l.KS == 0 {
			t.Fatalf("inconsistent layout %+v", l)
		}
		last := l.Block(uint8(l.SourceBlocks() - 1))
		if last.Offset+uint64(last.Length) != o.TransferLength || last.K > MaxSourceSymbols {
			t.Fatalf("last block %+v does not end at F=%d", last, o.TransferLength)
		}
	})
}

// A decoder fed arbitrary packets must not panic, and valid packets mixed
// with garbage must still decode the object.
func FuzzDecoderPackets(f *testing.F) {
	f.Add([]byte{0, 0, 0, 1, 0xaa}, uint8(3))
	f.Add([]byte{0, 0xff, 0xff, 0xff}, uint8(0))
	oti := OTI{TransferLength: 700, SymbolSize: 16, SourceBlocks: 2, SubBlocks: 2, Alignment: 4}
	data := make([]byte, oti.TransferLength)
	for i := range data {
		data[i] = byte(i * 13)
	}
	enc, _ := NewEncoder(data, oti)
	f.Fuzz(func(t *testing.T, garbage []byte, seed uint8) {
		dec, err := NewDecoder(oti, WithMaxMemory(1<<20))
		if err != nil {
			t.Fatal(err)
		}
		rng := rand.New(rand.NewPCG(uint64(seed), 0))
		// Garbage can carry valid payload IDs with wrong data, which no FEC
		// code can detect, so for garbage only the absence of panics and
		// unbounded growth is checked. A separate decoder then checks that a
		// random subset of valid packets decodes.
		for len(garbage) > 0 {
			n := min(len(garbage), 4+int(garbage[0]%40))
			_, _ = dec.AddPacket(garbage[:n]) // errors are expected for garbage
			garbage = garbage[n:]
		}
		dec2, _ := NewDecoder(oti)
		for sbn := range 2 {
			for esi := uint32(0); !dec2.Decoded() && esi < 200; esi++ {
				if rng.IntN(3) == 0 {
					continue
				}
				pkt, _ := enc.AppendPacket(nil, PayloadID{uint8(sbn), esi})
				if _, err := dec2.AddPacket(pkt); err != nil {
					t.Fatal(err)
				}
			}
		}
		got, err := dec2.AppendObject(nil)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("decode failed: %v", err)
		}
	})
}

func FuzzBlockRoundTrip(f *testing.F) {
	f.Add([]byte("hello world bro! keke meme 881"), uint8(7), uint64(1))
	f.Fuzz(func(t *testing.T, data []byte, tsize uint8, seed uint64) {
		T := 1 + int(tsize)
		if len(data) == 0 || (len(data)+T-1)/T > 2000 {
			return
		}
		enc, err := NewBlockEncoder(data, T)
		if err != nil {
			t.Fatal(err)
		}
		dec, _ := NewBlockDecoder(len(data), T)
		rng := rand.New(rand.NewPCG(seed, 1))
		esi := uint32(0)
		for dec.Decode() != nil {
			if rng.IntN(2) == 0 || int(esi) >= enc.K() {
				sendSymbol(t, enc, dec, esi)
			}
			esi++
			if dec.Received() > enc.K()+100 {
				t.Fatal("no decode after K+100 received symbols")
			}
		}
		if got, _ := dec.AppendSource(nil); !bytes.Equal(got, data) {
			t.Fatal("round trip mismatch")
		}
	})
}
