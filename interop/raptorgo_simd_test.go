//go:build goexperiment.simd

package interop

import (
	"testing"

	"github.com/mstephenholl/graptor-q"
)

// raptorgo v0.1.1's SIMD kernels are written against the Go 1.26 API of the
// experimental simd/archsimd package, which Go 1.27 changed (for example,
// LoadUint8x32Slice became LoadUint8x32). They do not build with
// GOEXPERIMENT=simd on the Go 1.27 toolchain that takeyourhatoff/raptorq
// requires, so builds with the experiment leave out raptorgo's tests and
// benchmarks.
func benchRaptorgo(*testing.B, string, int, graptorq.OTI, []byte, [][]byte) {}
