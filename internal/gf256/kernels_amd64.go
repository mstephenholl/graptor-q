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

//go:noescape
func hdpcStepAVX2(z, y, h1, h2 *byte, n int)

func hdpcStep(z, y, h1, h2 []byte) {
	n := len(z) &^ 31
	if n > 0 && active != &genericKernels {
		hdpcStepAVX2(&z[0], &y[0], &h1[0], &h2[0], n)
	} else {
		n = 0
	}
	if n < len(z) {
		hdpcStepGeneric(z[n:], y[n:], h1[n:], h2[n:])
	}
}

// xorN dispatches the fused XOR of up to 8 sources.
func xorN(dst []byte, srcs [][]byte, acc bool) {
	if active == &genericKernels {
		xorNGeneric(dst, srcs, acc)
	} else {
		xorNAVX2Wrap(dst, srcs, acc) // the avx2 and gfni tiers
	}
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

// xorGatherAVX2 runs the fused kernel on the prefix of dst that is a
// multiple of 32 bytes, filling its pointer array straight from the
// strided operands, and the generic code on the rest.
func xorGatherAVX2(dst, first, base []byte, stride int, idx []uint16, acc bool) {
	n := len(dst)
	m := n &^ (32 - 1)
	if m > 0 {
		var ptrs [8]*byte
		k := 0
		if first != nil {
			ptrs[0] = &first[0]
			k = 1
		}
		a := acc
		for _, i := range idx {
			o := int(i) * stride
			_ = base[o+n-1] // the whole operand is in bounds
			ptrs[k] = &base[o]
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
	if m < n {
		var ft []byte
		if first != nil {
			ft = first[m:]
		}
		xorGatherGeneric(dst[m:], ft, base[m:], stride, idx, acc)
	}
}

func xorGather(dst, first, base []byte, stride int, idx []uint16, acc bool) {
	if active == &genericKernels {
		xorGatherGeneric(dst, first, base, stride, idx, acc)
	} else {
		xorGatherAVX2(dst, first, base, stride, idx, acc)
	}
}
