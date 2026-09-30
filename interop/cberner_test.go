package interop

import (
	"bytes"
	"encoding/hex"
	"math/rand/v2"
	"os"
	"os/exec"
	"testing"

	"github.com/mholland/graptorq"
	"github.com/mholland/graptorq/internal/rfc"
	"github.com/mholland/graptorq/internal/testutil"
)

// oracle returns the path of the rqoracle binary, skipping the test if the
// RQORACLE environment variable is not set.
func oracle(t *testing.T) string {
	path := os.Getenv("RQORACLE")
	if path == "" {
		t.Skip("RQORACLE not set (build tools/rqoracle and point RQORACLE at the binary)")
	}
	return path
}

var liveOTIs = []graptorq.OTI{
	{TransferLength: 1, SymbolSize: 4, SourceBlocks: 1, SubBlocks: 1, Alignment: 4},
	{TransferLength: 100_000, SymbolSize: 1280, SourceBlocks: 1, SubBlocks: 1, Alignment: 8},
	{TransferLength: 1_000_003, SymbolSize: 1024, SourceBlocks: 4, SubBlocks: 2, Alignment: 8},
	{TransferLength: 300_001, SymbolSize: 96, SourceBlocks: 9, SubBlocks: 5, Alignment: 4},
	{TransferLength: 2_000_000, SymbolSize: 64, SourceBlocks: 2, SubBlocks: 1, Alignment: 4}, // K = 15625
	{TransferLength: 255 * 40 * 16, SymbolSize: 16, SourceBlocks: 255, SubBlocks: 3, Alignment: 2},
}

// cberner encodes (with 20% of the packets lost), graptorq decodes.
func TestCbernerToGraptorq(t *testing.T) {
	bin := oracle(t)
	rng := rand.New(rand.NewPCG(31, 32))
	for i, oti := range liveOTIs {
		otiBytes, _ := oti.MarshalBinary()
		seed := uint64(100 + i)
		l, _ := oti.Layout()
		repair := uint64(max(l.KL, l.KS)/3 + 10) // covers the 20% loss
		out, err := exec.Command(bin, "encode", hex.EncodeToString(otiBytes), itoa(seed), itoa(repair)).Output()
		if err != nil {
			t.Fatalf("%+v: rqoracle encode: %v", oti, err)
		}
		dec, err := graptorq.NewDecoder(oti)
		if err != nil {
			t.Fatal(err)
		}
		for _, pkt := range testutil.SplitPackets(out) {
			if rng.Float64() < 0.2 {
				continue
			}
			if _, err := dec.AddPacket(pkt); err != nil {
				t.Fatal(err)
			}
		}
		got, err := dec.AppendObject(nil)
		if err != nil {
			t.Fatalf("%+v: %v", oti, err)
		}
		if !bytes.Equal(got, testutil.PatternData(int(oti.TransferLength), seed)) {
			t.Fatalf("%+v: decoded object differs", oti)
		}
	}
}

// graptorq encodes (dropping every fourth source packet and adding repair
// packets), cberner decodes.
func TestGraptorqToCberner(t *testing.T) {
	bin := oracle(t)
	for i, oti := range liveOTIs {
		seed := uint64(200 + i)
		data := testutil.PatternData(int(oti.TransferLength), seed)
		enc, err := graptorq.NewEncoder(data, oti)
		if err != nil {
			t.Fatal(err)
		}
		l := enc.Layout()
		var pkts [][]byte
		for sbn := range l.SourceBlocks() {
			K := l.Block(uint8(sbn)).K
			lost := 0
			for esi := range K {
				if esi%4 == 1 {
					lost++
					continue
				}
				pkt, _ := enc.AppendPacket(nil, graptorq.PayloadID{SBN: uint8(sbn), ESI: uint32(esi)})
				pkts = append(pkts, pkt)
			}
			for r := range lost + 3 {
				pkt, _ := enc.AppendPacket(nil, graptorq.PayloadID{SBN: uint8(sbn), ESI: uint32(K + 10 + r)})
				pkts = append(pkts, pkt)
			}
		}
		otiBytes, _ := oti.MarshalBinary()
		cmd := exec.Command(bin, "decode", hex.EncodeToString(otiBytes))
		cmd.Stdin = bytes.NewReader(testutil.JoinPackets(pkts))
		got, err := cmd.Output()
		if err != nil {
			t.Fatalf("%+v: rqoracle decode: %v", oti, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("%+v: cberner decoded graptorq packets incorrectly", oti)
		}
	}
}

// Every K' of Table 2 against cberner: the first two repair symbols of a
// single source block of K' symbols must be identical. This covers the 99 K'
// values where P is prime, for which xssnick deviates.
func TestCbernerAllKPrime(t *testing.T) {
	bin := oracle(t)
	for i, p := range rfc.All() {
		if testing.Short() && i%10 != 0 {
			continue
		}
		const T = 4
		oti := graptorq.OTI{TransferLength: uint64(p.KPrime * T), SymbolSize: T, SourceBlocks: 1, SubBlocks: 1, Alignment: 4}
		otiBytes, _ := oti.MarshalBinary()
		seed := uint64(1000 + i)
		out, err := exec.Command(bin, "encode", hex.EncodeToString(otiBytes), itoa(seed), "2").Output()
		if err != nil {
			t.Fatal(err)
		}
		pkts := testutil.SplitPackets(out)
		enc, _ := graptorq.NewEncoder(testutil.PatternData(p.KPrime*T, seed), oti)
		for _, pkt := range pkts[len(pkts)-2:] {
			id, _ := graptorq.ParsePayloadID(pkt)
			got, _ := enc.AppendSymbol(nil, id)
			if !bytes.Equal(got, pkt[4:]) {
				t.Fatalf("K'=%d ESI=%d: graptorq %x, cberner %x", p.KPrime, id.ESI, got, pkt[4:])
			}
		}
	}
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
