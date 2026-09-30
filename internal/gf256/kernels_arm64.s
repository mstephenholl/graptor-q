//go:build !purego

#include "textflag.h"

// All kernels require len(src) to be a positive multiple of 32 and
// len(dst) >= len(src). dst and src may be the same slice.

// func xorNEON(dst, src []byte)
TEXT ·xorNEON(SB), NOSPLIT, $0-48
	MOVD dst_base+0(FP), R0
	MOVD src_base+24(FP), R1
	MOVD src_len+32(FP), R2
	LSR  $5, R2, R2

xor64:
	CMP    $2, R2
	BLT    xor32
	VLD1.P 64(R1), [V0.B16, V1.B16, V2.B16, V3.B16]
	VLD1   (R0), [V4.B16, V5.B16, V6.B16, V7.B16]
	VEOR   V0.B16, V4.B16, V4.B16
	VEOR   V1.B16, V5.B16, V5.B16
	VEOR   V2.B16, V6.B16, V6.B16
	VEOR   V3.B16, V7.B16, V7.B16
	VST1.P [V4.B16, V5.B16, V6.B16, V7.B16], 64(R0)
	SUB    $2, R2
	B      xor64

xor32:
	CBZ    R2, xordone
	VLD1   (R1), [V0.B16, V1.B16]
	VLD1   (R0), [V4.B16, V5.B16]
	VEOR   V0.B16, V4.B16, V4.B16
	VEOR   V1.B16, V5.B16, V5.B16
	VST1   [V4.B16, V5.B16], (R0)

xordone:
	RET

// Nibble-table multiplication: c*x = lo[x & 15] ^ hi[x >> 4] via TBL.

// func mulNEON(dst, src []byte, tbl *[32]byte)
TEXT ·mulNEON(SB), NOSPLIT, $0-56
	MOVD tbl+48(FP), R3
	VLD1 (R3), [V16.B16, V17.B16]
	MOVD $15, R4
	VDUP R4, V18.B16
	MOVD dst_base+0(FP), R0
	MOVD src_base+24(FP), R1
	MOVD src_len+32(FP), R2
	LSR  $5, R2, R2

mulloop:
	VLD1.P 32(R1), [V0.B16, V1.B16]
	VUSHR  $4, V0.B16, V2.B16
	VUSHR  $4, V1.B16, V3.B16
	VAND   V18.B16, V0.B16, V0.B16
	VAND   V18.B16, V1.B16, V1.B16
	VTBL   V0.B16, [V16.B16], V0.B16
	VTBL   V1.B16, [V16.B16], V1.B16
	VTBL   V2.B16, [V17.B16], V2.B16
	VTBL   V3.B16, [V17.B16], V3.B16
	VEOR   V2.B16, V0.B16, V0.B16
	VEOR   V3.B16, V1.B16, V1.B16
	VST1.P [V0.B16, V1.B16], 32(R0)
	SUBS   $1, R2, R2
	BNE    mulloop
	RET

// func mulAddNEON(dst, src []byte, tbl *[32]byte)
TEXT ·mulAddNEON(SB), NOSPLIT, $0-56
	MOVD tbl+48(FP), R3
	VLD1 (R3), [V16.B16, V17.B16]
	MOVD $15, R4
	VDUP R4, V18.B16
	MOVD dst_base+0(FP), R0
	MOVD src_base+24(FP), R1
	MOVD src_len+32(FP), R2
	LSR  $5, R2, R2

maddloop:
	VLD1.P 32(R1), [V0.B16, V1.B16]
	VUSHR  $4, V0.B16, V2.B16
	VUSHR  $4, V1.B16, V3.B16
	VAND   V18.B16, V0.B16, V0.B16
	VAND   V18.B16, V1.B16, V1.B16
	VTBL   V0.B16, [V16.B16], V0.B16
	VTBL   V1.B16, [V16.B16], V1.B16
	VTBL   V2.B16, [V17.B16], V2.B16
	VTBL   V3.B16, [V17.B16], V3.B16
	VEOR   V2.B16, V0.B16, V0.B16
	VEOR   V3.B16, V1.B16, V1.B16
	VLD1   (R0), [V4.B16, V5.B16]
	VEOR   V4.B16, V0.B16, V0.B16
	VEOR   V5.B16, V1.B16, V1.B16
	VST1.P [V0.B16, V1.B16], 32(R0)
	SUBS   $1, R2, R2
	BNE    maddloop
	RET

// Fused XOR of up to 8 sources, 32 bytes at a time: dst = src[0] ^ ... ^
// src[nsrc-1], or dst ^= src[0] ^ ... when acc is true.

// func xorNNEON(dst *byte, srcs *[8]*byte, nsrc int, n int, acc bool)
TEXT ·xorNNEON(SB), NOSPLIT, $0-33
	MOVD   dst+0(FP), R0
	MOVD   srcs+8(FP), R1
	MOVD   nsrc+16(FP), R2
	MOVD   n+24(FP), R3
	MOVBU  acc+32(FP), R4
	MOVD   $0, R5

xnloop:
	CMP    R3, R5
	BHS    xndone
	ADD    R0, R5, R6
	CBZ    R4, xnfirst
	VLD1   (R6), [V0.B16, V1.B16]
	MOVD   $0, R7
	B      xnsrc

xnfirst:
	MOVD   (R1), R8
	ADD    R8, R5, R8
	VLD1   (R8), [V0.B16, V1.B16]
	MOVD   $1, R7

xnsrc:
	CMP    R2, R7
	BHS    xnstore
	MOVD   (R1)(R7<<3), R8
	ADD    R8, R5, R8
	VLD1   (R8), [V2.B16, V3.B16]
	VEOR   V2.B16, V0.B16, V0.B16
	VEOR   V3.B16, V1.B16, V1.B16
	ADD    $1, R7
	B      xnsrc

xnstore:
	VST1   [V0.B16, V1.B16], (R6)
	ADD    $32, R5
	B      xnloop

xndone:
	RET
