package graptorq

import (
	"fmt"
	"testing"

	"github.com/mstephenholl/graptor-q/internal/testutil"
)

// BenchmarkDecodePaths compares the low-loss decoding path with the full
// solver for m missing source symbols (replaced by m+2 repair symbols). It
// is the basis of the lowLossWorthIt threshold.
func BenchmarkDecodePaths(b *testing.B) {
	for _, c := range []struct{ K, T int }{{100, 1280}, {1000, 64}, {1000, 1280}, {10000, 64}, {10000, 1280}, {50000, 256}} {
		data := testutil.PatternData(c.K*c.T, 1)
		enc, _ := NewBlockEncoder(data, c.T, WithConcurrency(1))
		if err := enc.Prepare(); err != nil {
			b.Fatal(err)
		}
		for _, m := range []int{1, 4, 16, 50, 100, 200, 400, 800} {
			if m >= c.K {
				continue
			}
			type sym struct {
				esi uint32
				b   []byte
			}
			var syms []sym
			for i := m; i < c.K; i++ {
				s, _ := enc.AppendSymbol(nil, uint32(i))
				syms = append(syms, sym{uint32(i), s})
			}
			for i := range m + 2 {
				s, _ := enc.AppendSymbol(nil, uint32(c.K+i))
				syms = append(syms, sym{uint32(c.K + i), s})
			}
			for _, mode := range []int{1, -1} {
				b.Run(fmt.Sprintf("K=%d/T=%d/m=%d/fast=%v", c.K, c.T, m, mode == 1), func(b *testing.B) {
					lowLossMode = mode
					defer func() { lowLossMode = 0 }()
					d, _ := NewBlockDecoder(len(data), c.T, WithConcurrency(1))
					for b.Loop() {
						d.Reset()
						for _, s := range syms {
							if _, err := d.AddSymbol(s.esi, s.b); err != nil {
								b.Fatal(err)
							}
						}
						if err := d.Decode(); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
