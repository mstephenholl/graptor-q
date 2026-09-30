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

//go:noescape
func hdpcStepNEON(z, y, h1, h2 *byte, n int)

func hdpcStep(z, y, h1, h2 []byte) {
	n := len(z) &^ 15
	if n > 0 && active != &genericKernels {
		hdpcStepNEON(&z[0], &y[0], &h1[0], &h2[0], n)
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
		xorNNEONWrap(dst, srcs, acc)
	}
}

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

// xorGatherNEON runs the fused kernel on the prefix of dst that is a
// multiple of 32 bytes, filling its pointer array straight from the
// strided operands, and the generic code on the rest.
func xorGatherNEON(dst, first, base []byte, stride int, idx []uint16, acc bool) {
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
				xorNNEON(&dst[0], &ptrs, 8, m, a)
				k, a = 0, true
			}
		}
		switch {
		case k > 0:
			xorNNEON(&dst[0], &ptrs, k, m, a)
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
		xorGatherNEON(dst, first, base, stride, idx, acc)
	}
}
