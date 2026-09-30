//go:build !purego

package gf256

import "github.com/mholland/graptorq/internal/cpu"

// Tiers, in increasing preference: ssse3 (SSE2 + PSHUFB, 16-byte vectors),
// avx2 (32-byte vectors) and gfni (avx2 with GF2P8AFFINEQB multiplication).

//go:noescape
func xorSSE2(dst, src []byte)

//go:noescape
func mulSSSE3(dst, src []byte, tbl *[32]byte)

//go:noescape
func mulAddSSSE3(dst, src []byte, tbl *[32]byte)

//go:noescape
func xorNSSE2(dst *byte, srcs *[8]*byte, nsrc int, n int, acc bool)

//go:noescape
func hdpcStepSSE2(z, y, h1, h2 *byte, n int)

//go:noescape
func xorAVX2(dst, src []byte)

//go:noescape
func mulAVX2(dst, src []byte, tbl *[32]byte)

//go:noescape
func mulAddAVX2(dst, src []byte, tbl *[32]byte)

//go:noescape
func xorNAVX2(dst *byte, srcs *[8]*byte, nsrc int, n int, acc bool)

//go:noescape
func hdpcStepAVX2(z, y, h1, h2 *byte, n int)

//go:noescape
func mulGFNI(dst, src []byte, m uint64)

//go:noescape
func mulAddGFNI(dst, src []byte, m uint64)

func archTiers() []*kernels {
	var t []*kernels
	if cpu.X86.HasSSSE3 {
		t = append(t, &ssse3Kernels)
	}
	if cpu.X86.HasAVX2 {
		t = append(t, &avx2Kernels)
		if cpu.X86.HasGFNI {
			t = append(t, &gfniKernels)
		}
	}
	return t
}

// Each kernel runs on the prefix of the slices that is a multiple of its
// block size (16 bytes for SSSE3, 32 for AVX2); the generic code handles the
// rest.

var ssse3Kernels = kernels{
	name: "ssse3",
	xor: func(dst, src []byte) {
		n := len(src) &^ 15
		if n > 0 {
			xorSSE2(dst[:n], src[:n])
		}
		if n < len(src) {
			xorGeneric(dst[n:], src[n:])
		}
	},
	mul: func(dst, src []byte, c byte) {
		n := len(src) &^ 15
		if n > 0 {
			mulSSSE3(dst[:n], src[:n], &nibbleTables[c])
		}
		if n < len(src) {
			mulGeneric(dst[n:], src[n:], c)
		}
	},
	mulAdd: func(dst, src []byte, c byte) {
		n := len(src) &^ 15
		if n > 0 {
			mulAddSSSE3(dst[:n], src[:n], &nibbleTables[c])
		}
		if n < len(src) {
			mulAddGeneric(dst[n:], src[n:], c)
		}
	},
}

var avx2Kernels = kernels{
	name: "avx2",
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

// The fused operations are dispatched with direct calls (not func values)
// so that their operands stay on the stack. The avx2 and gfni tiers share
// the AVX2 kernels.

func xorN(dst []byte, srcs [][]byte, acc bool) {
	switch active {
	case &genericKernels:
		xorNGeneric(dst, srcs, acc)
	case &ssse3Kernels:
		n := len(dst) &^ 15
		if n > 0 {
			var ptrs [8]*byte
			for i, s := range srcs {
				ptrs[i] = &s[0]
			}
			xorNSSE2(&dst[0], &ptrs, len(srcs), n, acc)
		}
		xorNTail(dst, srcs, acc, n)
	default:
		n := len(dst) &^ 31
		if n > 0 {
			var ptrs [8]*byte
			for i, s := range srcs {
				ptrs[i] = &s[0]
			}
			xorNAVX2(&dst[0], &ptrs, len(srcs), n, acc)
		}
		xorNTail(dst, srcs, acc, n)
	}
}

// xorNTail runs the generic fused XOR on bytes [n:] of the operands.
func xorNTail(dst []byte, srcs [][]byte, acc bool, n int) {
	if n < len(dst) {
		var tails [8][]byte
		for i, s := range srcs {
			tails[i] = s[n:]
		}
		xorNGeneric(dst[n:], tails[:len(srcs)], acc)
	}
}

func hdpcStep(z, y, h1, h2 []byte) {
	n := 0
	switch active {
	case &genericKernels:
	case &ssse3Kernels:
		if n = len(z) &^ 15; n > 0 {
			hdpcStepSSE2(&z[0], &y[0], &h1[0], &h2[0], n)
		}
	default:
		if n = len(z) &^ 31; n > 0 {
			hdpcStepAVX2(&z[0], &y[0], &h1[0], &h2[0], n)
		}
	}
	if n < len(z) {
		hdpcStepGeneric(z[n:], y[n:], h1[n:], h2[n:])
	}
}

func xorGather(dst, first, base []byte, stride int, idx []uint16, acc bool) {
	switch active {
	case &genericKernels:
		xorGatherGeneric(dst, first, base, stride, idx, acc)
	case &ssse3Kernels:
		m := len(dst) &^ 15
		if m > 0 {
			var ptrs [8]*byte
			k := 0
			if first != nil {
				ptrs[0] = &first[0]
				k = 1
			}
			a := acc
			for _, i := range idx {
				ptrs[k] = gatherOperand(base, int(i)*stride, len(dst))
				if k++; k == 8 {
					xorNSSE2(&dst[0], &ptrs, 8, m, a)
					k, a = 0, true
				}
			}
			switch {
			case k > 0:
				xorNSSE2(&dst[0], &ptrs, k, m, a)
			case !a:
				clear(dst[:m])
			}
		}
		xorGatherTail(dst, first, base, stride, idx, acc, m)
	default:
		m := len(dst) &^ 31
		if m > 0 {
			var ptrs [8]*byte
			k := 0
			if first != nil {
				ptrs[0] = &first[0]
				k = 1
			}
			a := acc
			for _, i := range idx {
				ptrs[k] = gatherOperand(base, int(i)*stride, len(dst))
				if k++; k == 8 {
					xorNAVX2(&dst[0], &ptrs, 8, m, a)
					k, a = 0, true
				}
			}
			switch {
			case k > 0:
				xorNAVX2(&dst[0], &ptrs, k, m, a)
			case !a:
				clear(dst[:m])
			}
		}
		xorGatherTail(dst, first, base, stride, idx, acc, m)
	}
}

// gatherOperand returns a pointer to the operand base[o:o+n], checking that
// all of it is in bounds.
func gatherOperand(base []byte, o, n int) *byte {
	_ = base[o+n-1]
	return &base[o]
}

// xorGatherTail runs the generic gather on bytes [m:] of the operands.
func xorGatherTail(dst, first, base []byte, stride int, idx []uint16, acc bool, m int) {
	if m < len(dst) {
		var ft []byte
		if first != nil {
			ft = first[m:]
		}
		xorGatherGeneric(dst[m:], ft, base[m:], stride, idx, acc)
	}
}
