//go:build !purego

package gf256

import "github.com/mstephenholl/graptor-q/internal/cpu"

//go:noescape
func xorNEON(dst, src []byte)

//go:noescape
func mulAddNEON(dst, src []byte, tbl *[32]byte)

// The NEON tier has kernels for xor and mulAdd, the two operations that
// Reed-Solomon parity uses; the other operations run the generic code.
var neonKernels = kernels{
	name: "neon",
	xor: func(dst, src []byte) {
		n := len(src) &^ 31
		if n > 0 {
			xorNEON(dst[:n], src[:n])
		}
		if n < len(src) {
			xorGeneric(dst[n:], src[n:])
		}
	},
	mul: mulGeneric,
	mulAdd: func(dst, src []byte, c byte) {
		n := len(src) &^ 31
		if n > 0 {
			mulAddNEON(dst[:n], src[:n], &nibbleTables[c])
		}
		if n < len(src) {
			mulAddGeneric(dst[n:], src[n:], c)
		}
	},
}

// NEON is optional on ARMv7, so the tier depends on the CPU reporting it.
func archTiers() []*kernels {
	if cpu.ARM.HasNEON {
		return []*kernels{&neonKernels}
	}
	return nil
}

func xorN(dst []byte, srcs [][]byte, acc bool) { xorNGeneric(dst, srcs, acc) }

func hdpcStep(z, y, h1, h2 []byte) { hdpcStepGeneric(z, y, h1, h2) }

func hdpcStepBits(z []byte, x []uint64, h1, h2 []byte) { hdpcStepBitsSpread(z, x, 0, h1, h2) }

func xorGather(dst, first, base []byte, stride int, idx []uint16, acc bool) {
	xorGatherGeneric(dst, first, base, stride, idx, acc)
}
