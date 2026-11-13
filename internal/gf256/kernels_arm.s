//go:build !purego

#include "textflag.h"

// Go's assembler has no NEON instructions for GOARCH=arm, so each one is a
// WORD whose comment is its GNU mnemonic. `make generate` (armwords) writes
// the encodings.
//
// Both kernels require len(src) to be a positive multiple of 32 and
// len(dst) >= len(src). dst and src may be the same slice. Go's ABI0 on arm
// preserves no floating-point registers, so any of D0-D31 may be used.

// func xorNEON(dst, src []byte)
TEXT ·xorNEON(SB), NOSPLIT, $0-24
	MOVW dst_base+0(FP), R0
	MOVW src_base+12(FP), R1
	MOVW src_len+16(FP), R2
	MOVW R2>>5, R2

xorloop:
	WORD  $0xf421020d // vld1.8 {d0-d3}, [r1]!
	WORD  $0xf420420f // vld1.8 {d4-d7}, [r0]
	WORD  $0xf3000154 // veor q0, q0, q2
	WORD  $0xf3022156 // veor q1, q1, q3
	WORD  $0xf400020d // vst1.8 {d0-d3}, [r0]!
	SUB.S $1, R2
	BNE   xorloop
	RET

// Nibble-table multiplication: c*x = lo[x & 15] ^ hi[x >> 4]. VTBL.8 looks
// up 8 indices in a 16-byte table held in two D registers.

// func mulAddNEON(dst, src []byte, tbl *[32]byte)
TEXT ·mulAddNEON(SB), NOSPLIT, $0-28
	MOVW tbl+24(FP), R3
	WORD $0xf463020f // vld1.8 {d16-d19}, [r3]
	WORD $0xf2c04e5f // vmov.i8 q10, #15
	MOVW dst_base+0(FP), R0
	MOVW src_base+12(FP), R1
	MOVW src_len+16(FP), R2
	MOVW R2>>5, R2

maddloop:
	WORD  $0xf421020d // vld1.8 {d0-d3}, [r1]!
	WORD  $0xf420420f // vld1.8 {d4-d7}, [r0]
	WORD  $0xf38c8050 // vshr.u8 q4, q0, #4
	WORD  $0xf38ca052 // vshr.u8 q5, q1, #4
	WORD  $0xf2000174 // vand q0, q0, q10
	WORD  $0xf2022174 // vand q1, q1, q10
	WORD  $0xf3b0c980 // vtbl.8 d12, {d16, d17}, d0
	WORD  $0xf3b0d981 // vtbl.8 d13, {d16, d17}, d1
	WORD  $0xf3b0e982 // vtbl.8 d14, {d16, d17}, d2
	WORD  $0xf3b0f983 // vtbl.8 d15, {d16, d17}, d3
	WORD  $0xf3f26988 // vtbl.8 d22, {d18, d19}, d8
	WORD  $0xf3f27989 // vtbl.8 d23, {d18, d19}, d9
	WORD  $0xf3f2898a // vtbl.8 d24, {d18, d19}, d10
	WORD  $0xf3f2998b // vtbl.8 d25, {d18, d19}, d11
	WORD  $0xf304415c // veor q2, q2, q6
	WORD  $0xf306615e // veor q3, q3, q7
	WORD  $0xf3044176 // veor q2, q2, q11
	WORD  $0xf3066178 // veor q3, q3, q12
	WORD  $0xf400420d // vst1.8 {d4-d7}, [r0]!
	SUB.S $1, R2
	BNE   maddloop
	RET
