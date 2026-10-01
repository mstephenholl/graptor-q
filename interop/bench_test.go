package interop

import (
	"fmt"
	"testing"

	"github.com/mstephenholl/graptor-q"
	"github.com/mstephenholl/graptor-q/internal/testutil"
	tyh "github.com/takeyourhatoff/raptorq"
	xraptorq "github.com/xssnick/raptorq"
)

// BenchmarkCmp compares the implementations on one source block of K symbols
// of T bytes (`make bench-compare`). Names are lib=<library>/op=<op>/K/T.
//
// op=encode creates an encoder for a new block and generates its first repair
// symbol, which computes the intermediate symbols. The solution procedure
// depends only on K', and libraries that can reuse it do so: graptorq through
// its plan cache, takeyourhatoff through Encoder.Reset. The -cold variants
// solve from scratch, like xssnick and raptorgo, which cannot reuse it.
//
// op=decode feeds a reused decoder the block with 10% of the source symbols
// lost and replaced by repair symbols, plus two extra, and decodes it into a
// reused buffer.
//
// op=object-encode and op=object-decode do the same through the object APIs,
// with an OTI describing a single source block: raptorgo has no block API.
// Its decoder cannot be reused, so it is created for every object and
// delivers a copy of it after a verifier (here a no-op) accepts it.
//
// takeyourhatoff and raptorgo have SIMD kernels, enabled by building with
// GOEXPERIMENT=simd (bench-compare runs both builds), but raptorgo's do not
// build with Go 1.27 (see raptorgo_simd_test.go).
func BenchmarkCmp(b *testing.B) {
	for _, c := range []struct{ K, T int }{{100, 1280}, {1000, 1280}, {10000, 1280}, {50000, 256}} {
		data := testutil.PatternData(c.K*c.T, 1)
		name := fmt.Sprintf("K=%d/T=%d", c.K, c.T)
		syms := decodeSymbols(c.K, c.T, data)
		benchEncode(b, name, c.K, c.T, data)
		benchDecode(b, name, c.T, data, syms)
		benchObject(b, name, c.K, c.T, data, syms)
	}
}

type symbol struct {
	esi  uint32
	data []byte
}

// decodeSymbols returns the symbols of the decode benchmarks: the source
// symbols except every tenth, then as many repair symbols as were lost plus
// two.
func decodeSymbols(K, T int, data []byte) []symbol {
	enc, _ := graptorq.NewBlockEncoder(data, T)
	var syms []symbol
	lost := 0
	for esi := range uint32(K) {
		if esi%10 == 3 {
			lost++
			continue
		}
		s, _ := enc.AppendSymbol(nil, esi)
		syms = append(syms, symbol{esi, s})
	}
	for i := range lost + 2 {
		esi := uint32(K + i)
		s, _ := enc.AppendSymbol(nil, esi)
		syms = append(syms, symbol{esi, s})
	}
	return syms
}

func benchEncode(b *testing.B, name string, K, T int, data []byte) {
	for _, v := range []struct {
		lib  string
		opts []graptorq.Option
	}{
		{"graptorq", nil},
		{"graptorq-cold", []graptorq.Option{graptorq.WithoutPlanCache()}},
		{"graptorq-par4", []graptorq.Option{graptorq.WithConcurrency(4)}},
	} {
		b.Run("lib="+v.lib+"/op=encode/"+name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				e, _ := graptorq.NewBlockEncoder(data, T, v.opts...)
				if _, err := e.AppendSymbol(nil, uint32(K)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	b.Run("lib=xssnick/op=encode/"+name, func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		for b.Loop() {
			e, _ := xraptorq.NewRaptorQ(uint32(T)).CreateEncoder(data)
			e.GenSymbol(uint32(K))
		}
	})
	b.Run("lib=takeyourhatoff/op=encode/"+name, func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		e, err := tyh.NewEncoder(data, T)
		if err != nil {
			b.Fatal(err)
		}
		sym := make([]byte, T)
		for b.Loop() {
			if err := e.Reset(data); err != nil {
				b.Fatal(err)
			}
			if err := e.Encode(sym, uint32(K)); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("lib=takeyourhatoff-cold/op=encode/"+name, func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		sym := make([]byte, T)
		for b.Loop() {
			e, err := tyh.NewEncoder(data, T)
			if err != nil {
				b.Fatal(err)
			}
			if err := e.Encode(sym, uint32(K)); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func benchDecode(b *testing.B, name string, T int, data []byte, syms []symbol) {
	for _, v := range []struct {
		lib string
		n   int
	}{{"graptorq", 1}, {"graptorq-par4", 4}} {
		b.Run("lib="+v.lib+"/op=decode/"+name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			d, _ := graptorq.NewBlockDecoder(len(data), T, graptorq.WithConcurrency(v.n))
			out := make([]byte, 0, len(data))
			for b.Loop() {
				d.Reset()
				for _, s := range syms {
					if _, err := d.AddSymbol(s.esi, s.data); err != nil {
						b.Fatal(err)
					}
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
		d, _ := xraptorq.NewRaptorQ(uint32(T)).CreateDecoder(uint32(len(data)))
		out := make([]byte, len(data))
		for b.Loop() {
			d.Reset()
			for _, s := range syms {
				if _, err := d.AddSymbol(s.esi, s.data); err != nil {
					b.Fatal(err)
				}
			}
			if ok, err := d.DecodeInto(out); !ok || err != nil {
				b.Fatal("xssnick decode failed", err)
			}
		}
	})
	b.Run("lib=takeyourhatoff/op=decode/"+name, func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		d, err := tyh.NewDecoder(len(data), T)
		if err != nil {
			b.Fatal(err)
		}
		out := make([]byte, len(data))
		for b.Loop() {
			d.Reset()
			for _, s := range syms {
				if _, err := d.Add(s.esi, s.data); err != nil {
					b.Fatal(err)
				}
			}
			if err := d.Decode(out); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func benchObject(b *testing.B, name string, K, T int, data []byte, syms []symbol) {
	oti := graptorq.OTI{TransferLength: uint64(len(data)), SymbolSize: uint16(T), SourceBlocks: 1, SubBlocks: 1, Alignment: 4}
	repair := graptorq.PayloadID{ESI: uint32(K)}
	for _, v := range []struct {
		lib  string
		opts []graptorq.Option
	}{{"graptorq", nil}, {"graptorq-cold", []graptorq.Option{graptorq.WithoutPlanCache()}}} {
		b.Run("lib="+v.lib+"/op=object-encode/"+name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				e, err := graptorq.NewEncoder(data, oti, v.opts...)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := e.AppendPacket(nil, repair); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	pkts := make([][]byte, len(syms))
	for i, s := range syms {
		pkts[i], _ = graptorq.PayloadID{ESI: s.esi}.AppendBinary(nil)
		pkts[i] = append(pkts[i], s.data...)
	}
	b.Run("lib=graptorq/op=object-decode/"+name, func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		d, err := graptorq.NewDecoder(oti)
		if err != nil {
			b.Fatal(err)
		}
		out := make([]byte, 0, len(data))
		for b.Loop() {
			d.Reset()
			for _, p := range pkts {
				if _, err := d.AddPacket(p); err != nil {
					b.Fatal(err)
				}
			}
			if out, err = d.AppendObject(out[:0]); err != nil {
				b.Fatal(err)
			}
		}
	})
	benchRaptorgo(b, name, K, oti, data, pkts)
}
