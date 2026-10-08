// Package gf256 is arithmetic in GF(2^8), the field graptorq codes over,
// together with slice kernels that apply it to whole symbols.
//
// The field is defined by the polynomial x^8 + x^4 + x^3 + x^2 + 1 (Poly,
// 0x11D) with generator alpha = 2, as in RFC 6330 Section 5.7. Reed-Solomon
// codes over the same polynomial, such as NRL NORM's 8-bit code, use these
// functions as they are.
//
// The slice kernels use the fastest tier this CPU supports, chosen once at
// startup: GFNI, AVX2 or SSSE3 on amd64, NEON on arm64, and portable Go
// elsewhere or when built with the purego tag. The environment variable
// GRAPTORQ_GF256 names a tier to use instead, for example generic.
package gf256

import "github.com/mstephenholl/graptor-q/internal/gf256"

// Poly is the reduction polynomial of the field.
const Poly = gf256.Poly

// Mul returns a * b.
func Mul(a, b byte) byte { return gf256.Mul(a, b) }

// Div returns a / b. It panics if b is zero.
func Div(a, b byte) byte { return gf256.Div(a, b) }

// Inv returns the multiplicative inverse of a. It panics if a is zero.
func Inv(a byte) byte { return gf256.Inv(a) }

// Exp returns alpha^n for n >= 0.
func Exp(n int) byte { return gf256.Exp(n) }

// Log returns log_alpha(a) in [0, 255). It panics if a is zero.
func Log(a byte) int { return gf256.Log(a) }

// AddSlice sets dst ^= src. dst and src must have the same length and may be
// the same slice, but must not otherwise overlap.
func AddSlice(dst, src []byte) { gf256.AddSlice(dst, src) }

// MulSlice sets dst = c * src. dst and src must have the same length and may
// be the same slice, but must not otherwise overlap.
func MulSlice(dst, src []byte, c byte) { gf256.MulSlice(dst, src, c) }

// MulAddSlice sets dst ^= c * src. dst and src must have the same length and
// may be the same slice, but must not otherwise overlap.
func MulAddSlice(dst, src []byte, c byte) { gf256.MulAddSlice(dst, src, c) }

// Tiers returns the names of the kernel tiers this machine supports,
// "generic" first. It is meant for tests and benchmarks.
func Tiers() []string { return gf256.Tiers() }

// Active returns the name of the kernel tier in use.
func Active() string { return gf256.Active() }

// Use switches the slice kernels to the named tier and returns a function
// that restores the previous one. It is meant for tests and benchmarks, must
// not run concurrently with the slice kernels, and panics on a tier Tiers
// does not list.
func Use(name string) (restore func()) { return gf256.Use(name) }
