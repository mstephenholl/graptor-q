package graptorq_test

import (
	"fmt"
	"testing"

	"github.com/mstephenholl/graptor-q"
	"github.com/mstephenholl/graptor-q/internal/gf256"
)

// BenchmarkGate is the suite the perf check compares between a pull request
// and its base (.github/workflows/perf.yml). It measures what docs/performance.md
// reports for graptor-q, one block on one goroutine, plus the GF(256) kernel
// of every tier this CPU supports, so that a slower tier is caught even when
// the runner's CPU would not select it.
//
// Each case does its own setup inside b.Run, so a run filtered to one case
// pays only that case's setup: the perf check starts a process per case.
//
// The file uses only the public API and gf256's tier switch, so that the
// calibration workflow can copy it onto older commits. Renaming a case makes
// the perf check skip it for one pull request (it compares names present on
// both sides).
func BenchmarkGate(b *testing.B) {
	sizes := []struct{ K, T int }{{100, 1280}, {1000, 1280}, {10000, 1280}, {50000, 256}}
	for _, c := range sizes {
		name := fmt.Sprintf("K=%d/T=%d", c.K, c.T)
		b.Run("op=encode/"+name, func(b *testing.B) {
			data := pattern(c.K * c.T)
			// Fill the package-wide plan cache before timing.
			warm, _ := graptorq.NewBlockEncoder(data, c.T, graptorq.WithConcurrency(1))
			if err := warm.Prepare(); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				e, _ := graptorq.NewBlockEncoder(data, c.T, graptorq.WithConcurrency(1))
				if _, err := e.AppendSymbol(nil, uint32(c.K)); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("op=encode-cold/"+name, func(b *testing.B) {
			data := pattern(c.K * c.T)
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				e, _ := graptorq.NewBlockEncoder(data, c.T, graptorq.WithConcurrency(1), graptorq.WithoutPlanCache())
				if _, err := e.AppendSymbol(nil, uint32(c.K)); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("op=decode/"+name, func(b *testing.B) {
			benchDecode(b, c.K, c.T, func(esi int) bool { return esi%10 == 3 })
		})
	}
	// Few losses in a large block: the low-loss path.
	b.Run("op=decode-lowloss/K=10000/T=1280", func(b *testing.B) {
		benchDecode(b, 10000, 1280, func(esi int) bool { return esi%2500 == 7 })
	})
	for _, tier := range gf256.Tiers() {
		b.Run("op=muladd/tier="+tier+"/n=1280", func(b *testing.B) {
			defer gf256.Use(tier)()
			src, dst := pattern(1280), make([]byte, 1280)
			b.SetBytes(1280)
			for b.Loop() {
				gf256.MulAddSlice(dst, src, 0x53)
			}
		})
	}
}

// benchDecode decodes a K-symbol block from which the source symbols
// selected by lost are missing, replaced by as many repair symbols plus two,
// into a reused decoder and buffer.
func benchDecode(b *testing.B, K, T int, lost func(esi int) bool) {
	data := pattern(K * T)
	enc, _ := graptorq.NewBlockEncoder(data, T, graptorq.WithConcurrency(1))
	type symbol struct {
		esi  uint32
		data []byte
	}
	var syms []symbol
	missing := 0
	for esi := range K {
		if lost(esi) {
			missing++
			continue
		}
		s, _ := enc.AppendSymbol(nil, uint32(esi))
		syms = append(syms, symbol{uint32(esi), s})
	}
	for i := range missing + 2 {
		s, _ := enc.AppendSymbol(nil, uint32(K+i))
		syms = append(syms, symbol{uint32(K + i), s})
	}
	d, _ := graptorq.NewBlockDecoder(len(data), T, graptorq.WithConcurrency(1))
	out := make([]byte, 0, len(data))
	b.SetBytes(int64(len(data)))
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
}

// pattern returns n bytes that are not all alike, without depending on
// internal/testutil (which older commits may lack).
func pattern(n int) []byte {
	p := make([]byte, n)
	x := uint32(1)
	for i := range p {
		x = x*1664525 + 1013904223
		p[i] = byte(x >> 24)
	}
	return p
}
