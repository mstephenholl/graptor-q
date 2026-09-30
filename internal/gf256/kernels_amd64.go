//go:build !purego

package gf256

import "github.com/mholland/graptorq/internal/cpu"

//go:noescape
func xorAVX2(dst, src []byte)

//go:noescape
func mulAVX2(dst, src []byte, tbl *[32]byte)

//go:noescape
func mulAddAVX2(dst, src []byte, tbl *[32]byte)

//go:noescape
func xorNAVX2(dst *byte, srcs *[8]*byte, nsrc int, n int, acc bool)

//go:noescape
func mulGFNI(dst, src []byte, m uint64)

//go:noescape
func mulAddGFNI(dst, src []byte, m uint64)

// xorNAVX2Wrap runs the fused XOR kernel on the multiple-of-32 prefix and
// the generic code on the tail. The kernel is called directly (not through a
// func value) so that ptrs stays on the stack.
func xorNAVX2Wrap(dst []byte, srcs [][]byte, acc bool) {
	n := len(dst) &^ 31
	if n > 0 {
		var ptrs [8]*byte
		for i, s := range srcs {
			ptrs[i] = &s[0]
		}
		xorNAVX2(&dst[0], &ptrs, len(srcs), n, acc)
	}
	if n < len(dst) {
		var tails [8][]byte
		for i, s := range srcs {
			tails[i] = s[n:]
		}
		xorNGeneric(dst[n:], tails[:len(srcs)], acc)
	}
}

var avx2Kernels = kernels{
	name: "avx2",
	xorN: xorNAVX2Wrap,
	xor: func(dst, src []byte) {
		n := len(src) &^ 31
		if n > 0 {
			xorAVX2(dst[:n], src[:n])
		}
		if n < len(src) {
			xorGeneric(dst[n:], src[n:])
		}
	},
	mul: func(dst, src []byte, c byte) {
		n := len(src) &^ 31
		if n > 0 {
			mulAVX2(dst[:n], src[:n], &nibbleTables[c])
		}
		if n < len(src) {
			mulGeneric(dst[n:], src[n:], c)
		}
	},
	mulAdd: func(dst, src []byte, c byte) {
		n := len(src) &^ 31
		if n > 0 {
			mulAddAVX2(dst[:n], src[:n], &nibbleTables[c])
		}
		if n < len(src) {
			mulAddGeneric(dst[n:], src[n:], c)
		}
	},
}

var gfniKernels = kernels{
	name: "gfni",
	xor:  avx2Kernels.xor,
	xorN: xorNAVX2Wrap,
	mul: func(dst, src []byte, c byte) {
		n := len(src) &^ 31
		if n > 0 {
			mulGFNI(dst[:n], src[:n], gfniMatrix[c])
		}
		if n < len(src) {
			mulGeneric(dst[n:], src[n:], c)
		}
	},
	mulAdd: func(dst, src []byte, c byte) {
		n := len(src) &^ 31
		if n > 0 {
			mulAddGFNI(dst[:n], src[:n], gfniMatrix[c])
		}
		if n < len(src) {
			mulAddGeneric(dst[n:], src[n:], c)
		}
	},
}

func archTiers() []*kernels {
	var t []*kernels
	if cpu.X86.HasAVX2 {
		t = append(t, &avx2Kernels)
		if cpu.X86.HasGFNI {
			t = append(t, &gfniKernels)
		}
	}
	return t
}
