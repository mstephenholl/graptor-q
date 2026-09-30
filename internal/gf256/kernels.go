package gf256

import (
	"fmt"
	"os"
)

// kernels is one implementation tier of the slice operations. All functions
// require len(dst) == len(src); dst and src may be the same slice but must
// not otherwise overlap. mul and mulAdd are only called with c > 1.
type kernels struct {
	name   string
	xor    func(dst, src []byte)         // dst ^= src
	mul    func(dst, src []byte, c byte) // dst = c * src
	mulAdd func(dst, src []byte, c byte) // dst ^= c * src
	// xorN sets dst = srcs[0] ^ ... ^ srcs[n-1] (or dst ^= ... when acc)
	// for 1 <= n <= 8 sources, reading each source once.
	xorN func(dst []byte, srcs [][]byte, acc bool)
}

var (
	tiers  []*kernels // generic first, then architecture tiers in increasing preference
	active *kernels
)

func init() {
	tiers = append([]*kernels{&genericKernels}, archTiers()...)
	active = tiers[len(tiers)-1]
	if name := os.Getenv("GRAPTORQ_GF256"); name != "" {
		for _, k := range tiers {
			if k.name == name {
				active = k
			}
		}
	}
}

// Tiers returns the names of the kernel tiers supported on this machine,
// "generic" first. It is intended for tests and benchmarks.
func Tiers() []string {
	names := make([]string, len(tiers))
	for i, k := range tiers {
		names[i] = k.name
	}
	return names
}

// Active returns the name of the kernel tier in use.
func Active() string { return active.name }

// Use switches to the named kernel tier and returns a function restoring the
// previous one. It is intended for tests and benchmarks and is not safe for
// concurrent use with the slice operations. It panics on an unknown tier.
func Use(name string) (restore func()) {
	prev := active
	for _, k := range tiers {
		if k.name == name {
			active = k
			return func() { active = prev }
		}
	}
	panic(fmt.Sprintf("gf256: unsupported kernel tier %q", name))
}

func checkLen(dst, src []byte) {
	if len(dst) != len(src) {
		panic(fmt.Sprintf("gf256: length mismatch (dst %d, src %d)", len(dst), len(src)))
	}
}

// AddSlice sets dst ^= src (symbol addition, Section 5.7.5).
func AddSlice(dst, src []byte) {
	checkLen(dst, src)
	active.xor(dst, src)
}

// MulSlice sets dst = c * src. dst and src may be the same slice.
func MulSlice(dst, src []byte, c byte) {
	checkLen(dst, src)
	switch c {
	case 0:
		clear(dst)
	case 1:
		copy(dst, src)
	default:
		active.mul(dst, src, c)
	}
}

// MulAddSlice sets dst ^= c * src.
func MulAddSlice(dst, src []byte, c byte) {
	checkLen(dst, src)
	switch c {
	case 0:
	case 1:
		active.xor(dst, src)
	default:
		active.mulAdd(dst, src, c)
	}
}

// ScaleAdd sets dst = c*dst ^ src.
func ScaleAdd(dst, src []byte, c byte) {
	checkLen(dst, src)
	switch c {
	case 0:
		copy(dst, src)
	case 1:
		active.xor(dst, src)
	default:
		active.mul(dst, dst, c)
		active.xor(dst, src)
	}
}

// SetXor sets dst = srcs[0] ^ srcs[1] ^ ..., reading every source once; with
// no sources dst is cleared. Sources must not overlap dst.
func SetXor(dst []byte, srcs [][]byte) {
	if len(srcs) == 0 {
		clear(dst)
		return
	}
	xorGroups(dst, srcs, false)
}

// XorN sets dst ^= srcs[0] ^ srcs[1] ^ ... Sources must not overlap dst.
func XorN(dst []byte, srcs [][]byte) {
	if len(srcs) > 0 {
		xorGroups(dst, srcs, true)
	}
}

func xorGroups(dst []byte, srcs [][]byte, acc bool) {
	for _, s := range srcs {
		checkLen(dst, s)
	}
	for len(srcs) > 0 {
		n := min(len(srcs), 8)
		active.xorN(dst, srcs[:n], acc)
		srcs, acc = srcs[n:], true
	}
}
