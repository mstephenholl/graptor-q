//go:build !purego

package gf256

//go:noescape
func xorNEON(dst, src []byte)

//go:noescape
func mulNEON(dst, src []byte, tbl *[32]byte)

//go:noescape
func mulAddNEON(dst, src []byte, tbl *[32]byte)

//go:noescape
func xorNNEON(dst *byte, srcs *[8]*byte, nsrc int, n int, acc bool)

func xorNNEONWrap(dst []byte, srcs [][]byte, acc bool) {
	n := len(dst) &^ 31
	if n > 0 {
		var ptrs [8]*byte
		for i, s := range srcs {
			ptrs[i] = &s[0]
		}
		xorNNEON(&dst[0], &ptrs, len(srcs), n, acc)
	}
	if n < len(dst) {
		var tails [8][]byte
		for i, s := range srcs {
			tails[i] = s[n:]
		}
		xorNGeneric(dst[n:], tails[:len(srcs)], acc)
	}
}

var neonKernels = kernels{
	name: "neon",
	xorN: xorNNEONWrap,
	xor: func(dst, src []byte) {
		n := len(src) &^ 31
		if n > 0 {
			xorNEON(dst[:n], src[:n])
		}
		if n < len(src) {
			xorGeneric(dst[n:], src[n:])
		}
	},
	mul: func(dst, src []byte, c byte) {
		n := len(src) &^ 31
		if n > 0 {
			mulNEON(dst[:n], src[:n], &nibbleTables[c])
		}
		if n < len(src) {
			mulGeneric(dst[n:], src[n:], c)
		}
	},
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

// Advanced SIMD (NEON) is part of the arm64 baseline.
func archTiers() []*kernels { return []*kernels{&neonKernels} }
