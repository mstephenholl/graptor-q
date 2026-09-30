package graptorq_test

import (
	"bytes"
	"fmt"
	"log"

	"github.com/mholland/graptorq"
)

// Sending an object over a lossy channel: the sender derives the OTI (which
// the receiver needs, e.g. from a session description), sends the source
// packets and some repair packets, and the receiver decodes from whatever
// arrives.
func Example() {
	object := bytes.Repeat([]byte("RaptorQ is a fountain code. "), 2000)

	oti, err := graptorq.DeriveOTI(uint64(len(object)), graptorq.Config{PayloadSize: 1024})
	if err != nil {
		log.Fatal(err)
	}
	enc, err := graptorq.NewEncoder(object, oti)
	if err != nil {
		log.Fatal(err)
	}
	dec, err := graptorq.NewDecoder(oti)
	if err != nil {
		log.Fatal(err)
	}

	sent, lost := 0, 0
	for id, symbol := range enc.Packets(20) { // K source + 20 repair symbols per block
		sent++
		if sent%5 == 0 { // every fifth packet is lost
			lost++
			continue
		}
		pkt, _ := id.AppendBinary(nil)
		pkt = append(pkt, symbol...)
		if done, err := dec.AddPacket(pkt); err != nil {
			log.Fatal(err)
		} else if done {
			break
		}
	}
	got, err := dec.AppendObject(nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("OTI %+v\n", oti)
	fmt.Println("lost:", lost, "recovered:", bytes.Equal(got, object))
	// Output:
	// OTI {TransferLength:56000 SymbolSize:1024 SourceBlocks:1 SubBlocks:1 Alignment:4}
	// lost: 13 recovered: true
}

// A single source block: encode, lose the first half of the source symbols,
// and decode from the rest plus repair symbols.
func ExampleBlockEncoder() {
	data := []byte("The quick brown fox jumps over the lazy dog, again and again and again.")
	const symbolSize = 8

	enc, err := graptorq.NewBlockEncoder(data, symbolSize)
	if err != nil {
		log.Fatal(err)
	}
	dec, err := graptorq.NewBlockDecoder(len(data), symbolSize)
	if err != nil {
		log.Fatal(err)
	}
	for esi := uint32(enc.K() / 2); dec.Decode() != nil; esi++ {
		symbol, err := enc.AppendSymbol(nil, esi)
		if err != nil {
			log.Fatal(err)
		}
		dec.AddSymbol(esi, symbol)
	}
	got, _ := dec.AppendSource(nil)
	fmt.Printf("K=%d K'=%d received=%d\n%s\n", enc.K(), enc.KPrime(), dec.Received(), got)
	// Output:
	// K=9 K'=10 received=9
	// The quick brown fox jumps over the lazy dog, again and again and again.
}
