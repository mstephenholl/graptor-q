// Package cpu detects the CPU features used by the SIMD kernels.
//
// golang.org/x/sys/cpu cannot be used: it only reports GFNI together with
// AVX-512, while the kernels need the VEX-encoded GFNI instructions that CPUs
// with AVX2 and GFNI but no AVX-512 also provide.
package cpu

// X86 holds the x86 features relevant to graptorq. All fields are false on
// other architectures and when built with the purego tag.
var X86 struct {
	HasSSSE3 bool // SSSE3 (PSHUFB); SSE2 is part of the amd64 baseline
	HasAVX2  bool // AVX2 with OS support for YMM state
	HasGFNI  bool // GFNI (usable with VEX-encoded YMM operands when HasAVX2)
}
