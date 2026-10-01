//go:build !purego

#include "textflag.h"

// All kernels require len(src) to be a positive multiple of 32 and
// len(dst) >= len(src). dst and src may be the same slice.
//
// Only VEX-encoded instructions touch vector registers: a legacy SSE
// instruction (such as MOVQ r64, xmm) executed while the upper YMM halves are
// dirty costs hundreds of cycles on some Intel cores.

// func xorAVX2(dst, src []byte)
TEXT ·xorAVX2(SB), NOSPLIT, $0-48
	MOVQ dst_base+0(FP), DI
	MOVQ src_base+24(FP), SI
	MOVQ src_len+32(FP), CX
	SHRQ $5, CX

loop128:
	CMPQ CX, $4
	JB   loop32
	VMOVDQU (SI), Y0
	VMOVDQU 32(SI), Y1
	VMOVDQU 64(SI), Y2
	VMOVDQU 96(SI), Y3
	VPXOR   (DI), Y0, Y0
	VPXOR   32(DI), Y1, Y1
	VPXOR   64(DI), Y2, Y2
	VPXOR   96(DI), Y3, Y3
	VMOVDQU Y0, (DI)
	VMOVDQU Y1, 32(DI)
	VMOVDQU Y2, 64(DI)
	VMOVDQU Y3, 96(DI)
	ADDQ    $128, SI
	ADDQ    $128, DI
	SUBQ    $4, CX
	JMP     loop128

loop32:
	TESTQ   CX, CX
	JZ      done
	VMOVDQU (SI), Y0
	VPXOR   (DI), Y0, Y0
	VMOVDQU Y0, (DI)
	ADDQ    $32, SI
	ADDQ    $32, DI
	DECQ    CX
	JMP     loop32

done:
	VZEROUPPER
	RET

// Nibble-table multiplication: c*x = lo[x & 15] ^ hi[x >> 4], with the two
// 16-byte tables broadcast to both 128-bit lanes (VPSHUFB is per lane).

// func mulAVX2(dst, src []byte, tbl *[32]byte)
TEXT ·mulAVX2(SB), NOSPLIT, $0-56
	MOVQ           tbl+48(FP), AX
	MOVQ           $15, BX
	VMOVQ          BX, X8
	VPBROADCASTB   X8, Y8
	VBROADCASTI128 (AX), Y6
	VBROADCASTI128 16(AX), Y7
	MOVQ           dst_base+0(FP), DI
	MOVQ           src_base+24(FP), SI
	MOVQ           src_len+32(FP), CX
	SHRQ           $5, CX

mul64:
	CMPQ    CX, $2
	JB      mul32
	VMOVDQU (SI), Y0
	VMOVDQU 32(SI), Y1
	VPSRLQ  $4, Y0, Y2
	VPSRLQ  $4, Y1, Y3
	VPAND   Y8, Y0, Y0
	VPAND   Y8, Y1, Y1
	VPAND   Y8, Y2, Y2
	VPAND   Y8, Y3, Y3
	VPSHUFB Y0, Y6, Y0
	VPSHUFB Y1, Y6, Y1
	VPSHUFB Y2, Y7, Y2
	VPSHUFB Y3, Y7, Y3
	VPXOR   Y2, Y0, Y0
	VPXOR   Y3, Y1, Y1
	VMOVDQU Y0, (DI)
	VMOVDQU Y1, 32(DI)
	ADDQ    $64, SI
	ADDQ    $64, DI
	SUBQ    $2, CX
	JMP     mul64

mul32:
	TESTQ   CX, CX
	JZ      muldone
	VMOVDQU (SI), Y0
	VPSRLQ  $4, Y0, Y2
	VPAND   Y8, Y0, Y0
	VPAND   Y8, Y2, Y2
	VPSHUFB Y0, Y6, Y0
	VPSHUFB Y2, Y7, Y2
	VPXOR   Y2, Y0, Y0
	VMOVDQU Y0, (DI)

muldone:
	VZEROUPPER
	RET

// func mulAddAVX2(dst, src []byte, tbl *[32]byte)
TEXT ·mulAddAVX2(SB), NOSPLIT, $0-56
	MOVQ           tbl+48(FP), AX
	MOVQ           $15, BX
	VMOVQ          BX, X8
	VPBROADCASTB   X8, Y8
	VBROADCASTI128 (AX), Y6
	VBROADCASTI128 16(AX), Y7
	MOVQ           dst_base+0(FP), DI
	MOVQ           src_base+24(FP), SI
	MOVQ           src_len+32(FP), CX
	SHRQ           $5, CX

madd64:
	CMPQ    CX, $2
	JB      madd32
	VMOVDQU (SI), Y0
	VMOVDQU 32(SI), Y1
	VPSRLQ  $4, Y0, Y2
	VPSRLQ  $4, Y1, Y3
	VPAND   Y8, Y0, Y0
	VPAND   Y8, Y1, Y1
	VPAND   Y8, Y2, Y2
	VPAND   Y8, Y3, Y3
	VPSHUFB Y0, Y6, Y0
	VPSHUFB Y1, Y6, Y1
	VPSHUFB Y2, Y7, Y2
	VPSHUFB Y3, Y7, Y3
	VPXOR   Y2, Y0, Y0
	VPXOR   Y3, Y1, Y1
	VPXOR   (DI), Y0, Y0
	VPXOR   32(DI), Y1, Y1
	VMOVDQU Y0, (DI)
	VMOVDQU Y1, 32(DI)
	ADDQ    $64, SI
	ADDQ    $64, DI
	SUBQ    $2, CX
	JMP     madd64

madd32:
	TESTQ   CX, CX
	JZ      madddone
	VMOVDQU (SI), Y0
	VPSRLQ  $4, Y0, Y2
	VPAND   Y8, Y0, Y0
	VPAND   Y8, Y2, Y2
	VPSHUFB Y0, Y6, Y0
	VPSHUFB Y2, Y7, Y2
	VPXOR   Y2, Y0, Y0
	VPXOR   (DI), Y0, Y0
	VMOVDQU Y0, (DI)

madddone:
	VZEROUPPER
	RET

// GFNI multiplication: GF2P8AFFINEQB applies the 8x8 bit matrix of
// multiplication by c (in the field 0x11D) to every byte. GF2P8MULB cannot be
// used: it is hard-wired to the AES polynomial 0x11B.

// func mulGFNI(dst, src []byte, m uint64)
TEXT ·mulGFNI(SB), NOSPLIT, $0-56
	MOVQ         m+48(FP), AX
	VMOVQ        AX, X6
	VPBROADCASTQ X6, Y6
	MOVQ         dst_base+0(FP), DI
	MOVQ         src_base+24(FP), SI
	MOVQ         src_len+32(FP), CX
	SHRQ         $5, CX

gmul64:
	CMPQ           CX, $2
	JB             gmul32
	VMOVDQU        (SI), Y0
	VMOVDQU        32(SI), Y1
	VGF2P8AFFINEQB $0, Y6, Y0, Y0
	VGF2P8AFFINEQB $0, Y6, Y1, Y1
	VMOVDQU        Y0, (DI)
	VMOVDQU        Y1, 32(DI)
	ADDQ           $64, SI
	ADDQ           $64, DI
	SUBQ           $2, CX
	JMP            gmul64

gmul32:
	TESTQ          CX, CX
	JZ             gmuldone
	VMOVDQU        (SI), Y0
	VGF2P8AFFINEQB $0, Y6, Y0, Y0
	VMOVDQU        Y0, (DI)

gmuldone:
	VZEROUPPER
	RET

// func mulAddGFNI(dst, src []byte, m uint64)
TEXT ·mulAddGFNI(SB), NOSPLIT, $0-56
	MOVQ         m+48(FP), AX
	VMOVQ        AX, X6
	VPBROADCASTQ X6, Y6
	MOVQ         dst_base+0(FP), DI
	MOVQ         src_base+24(FP), SI
	MOVQ         src_len+32(FP), CX
	SHRQ         $5, CX

gmadd128:
	CMPQ           CX, $4
	JB             gmadd32
	VMOVDQU        (SI), Y0
	VMOVDQU        32(SI), Y1
	VMOVDQU        64(SI), Y2
	VMOVDQU        96(SI), Y3
	VGF2P8AFFINEQB $0, Y6, Y0, Y0
	VGF2P8AFFINEQB $0, Y6, Y1, Y1
	VGF2P8AFFINEQB $0, Y6, Y2, Y2
	VGF2P8AFFINEQB $0, Y6, Y3, Y3
	VPXOR          (DI), Y0, Y0
	VPXOR          32(DI), Y1, Y1
	VPXOR          64(DI), Y2, Y2
	VPXOR          96(DI), Y3, Y3
	VMOVDQU        Y0, (DI)
	VMOVDQU        Y1, 32(DI)
	VMOVDQU        Y2, 64(DI)
	VMOVDQU        Y3, 96(DI)
	ADDQ           $128, SI
	ADDQ           $128, DI
	SUBQ           $4, CX
	JMP            gmadd128

gmadd32:
	TESTQ          CX, CX
	JZ             gmadddone
	VMOVDQU        (SI), Y0
	VGF2P8AFFINEQB $0, Y6, Y0, Y0
	VPXOR          (DI), Y0, Y0
	VMOVDQU        Y0, (DI)
	ADDQ           $32, SI
	ADDQ           $32, DI
	DECQ           CX
	JMP            gmadd32

gmadddone:
	VZEROUPPER
	RET

// Fused XOR of up to 8 sources: for each 32-byte block, dst = src[0] ^ ...
// ^ src[nsrc-1], or dst ^= src[0] ^ ... when acc is true. Every source is
// read once and dst is written once, instead of one pass per source.

// func xorNAVX2(dst *byte, srcs *[8]*byte, nsrc int, n int, acc bool)
TEXT ·xorNAVX2(SB), NOSPLIT, $0-33
	MOVQ    dst+0(FP), DI
	MOVQ    srcs+8(FP), R8
	MOVQ    nsrc+16(FP), R9
	MOVQ    n+24(FP), CX
	MOVBLZX acc+32(FP), R10
	XORQ    AX, AX

xn64:
	LEAQ    64(AX), DX
	CMPQ    DX, CX
	JA      xn32
	TESTQ   R10, R10
	JZ      xn64first
	VMOVDQU (DI)(AX*1), Y0
	VMOVDQU 32(DI)(AX*1), Y1
	XORQ    BX, BX
	JMP     xn64src

xn64first:
	MOVQ    (R8), SI
	VMOVDQU (SI)(AX*1), Y0
	VMOVDQU 32(SI)(AX*1), Y1
	MOVQ    $1, BX

xn64src:
	CMPQ  BX, R9
	JAE   xn64store
	MOVQ  (R8)(BX*8), SI
	VPXOR (SI)(AX*1), Y0, Y0
	VPXOR 32(SI)(AX*1), Y1, Y1
	INCQ  BX
	JMP   xn64src

xn64store:
	VMOVDQU Y0, (DI)(AX*1)
	VMOVDQU Y1, 32(DI)(AX*1)
	MOVQ    DX, AX
	JMP     xn64

xn32:
	CMPQ    AX, CX
	JAE     xndone
	TESTQ   R10, R10
	JZ      xn32first
	VMOVDQU (DI)(AX*1), Y0
	XORQ    BX, BX
	JMP     xn32src

xn32first:
	MOVQ    (R8), SI
	VMOVDQU (SI)(AX*1), Y0
	MOVQ    $1, BX

xn32src:
	CMPQ  BX, R9
	JAE   xn32store
	MOVQ  (R8)(BX*8), SI
	VPXOR (SI)(AX*1), Y0, Y0
	INCQ  BX
	JMP   xn32src

xn32store:
	VMOVDQU Y0, (DI)(AX*1)

xndone:
	VZEROUPPER
	RET

// One step of the HDPC recurrence, 32 bytes at a time: z = alpha*z ^ y,
// h1 ^= z, h2 ^= z. alpha*z doubles each byte (VPADDB) and XORs 0x1D into
// the bytes whose high bit was set (VPCMPGTB against zero gives that mask).
// h1 and h2 may be the same buffer.

// func hdpcStepAVX2(z, y, h1, h2 *byte, n int)
TEXT ·hdpcStepAVX2(SB), NOSPLIT, $0-40
	MOVQ         z+0(FP), DI
	MOVQ         y+8(FP), SI
	MOVQ         h1+16(FP), R8
	MOVQ         h2+24(FP), R9
	MOVQ         n+32(FP), CX
	MOVQ         $0x1d, AX
	VMOVQ        AX, X7
	VPBROADCASTB X7, Y7
	VPXOR        Y6, Y6, Y6
	XORQ         BX, BX

hdloop:
	VMOVDQU  (DI)(BX*1), Y0
	VPCMPGTB Y0, Y6, Y1
	VPADDB   Y0, Y0, Y0
	VPAND    Y7, Y1, Y1
	VPXOR    Y1, Y0, Y0
	VPXOR    (SI)(BX*1), Y0, Y0
	VMOVDQU  Y0, (DI)(BX*1)
	VPXOR    (R8)(BX*1), Y0, Y2
	VMOVDQU  Y2, (R8)(BX*1)
	VPXOR    (R9)(BX*1), Y0, Y3
	VMOVDQU  Y3, (R9)(BX*1)
	ADDQ     $32, BX
	CMPQ     BX, CX
	JB       hdloop
	VZEROUPPER
	RET

// Byte k of bitSpread is k/8, of bitMask 1<<(k%8): with the 4 bytes of a
// bitset broadcast to every dword, VPSHUFB (within 128-bit lanes) puts
// bitset byte k/8 in byte k, and masking isolates bit k.
DATA bitSpread<>+0(SB)/8, $0x0000000000000000
DATA bitSpread<>+8(SB)/8, $0x0101010101010101
DATA bitSpread<>+16(SB)/8, $0x0202020202020202
DATA bitSpread<>+24(SB)/8, $0x0303030303030303
GLOBL bitSpread<>(SB), RODATA|NOPTR, $32

DATA bitMask<>+0(SB)/8, $0x8040201008040201
DATA bitMask<>+8(SB)/8, $0x8040201008040201
DATA bitMask<>+16(SB)/8, $0x8040201008040201
DATA bitMask<>+24(SB)/8, $0x8040201008040201
GLOBL bitMask<>(SB), RODATA|NOPTR, $32

// func hdpcStepBitsAVX2(z *byte, x *uint64, h1, h2 *byte, n int)
// hdpcStepAVX2 with y given as a bitset: byte k of y is bit k of x. The
// bits are expanded in registers, 32 at a time.
TEXT ·hdpcStepBitsAVX2(SB), NOSPLIT, $0-40
	MOVQ         z+0(FP), DI
	MOVQ         x+8(FP), SI
	MOVQ         h1+16(FP), R8
	MOVQ         h2+24(FP), R9
	MOVQ         n+32(FP), CX
	MOVQ         $0x1d, AX
	VMOVQ        AX, X7
	VPBROADCASTB X7, Y7
	VPXOR        Y6, Y6, Y6
	VMOVDQU      bitSpread<>(SB), Y5
	VMOVDQU      bitMask<>(SB), Y4
	XORQ         BX, BX

hdbloop:
	VPBROADCASTD (SI), Y1
	VPSHUFB      Y5, Y1, Y1
	VPAND        Y4, Y1, Y1
	VPCMPEQB     Y4, Y1, Y1
	VPABSB       Y1, Y1
	VMOVDQU      (DI)(BX*1), Y0
	VPCMPGTB     Y0, Y6, Y2
	VPADDB       Y0, Y0, Y0
	VPAND        Y7, Y2, Y2
	VPXOR        Y2, Y0, Y0
	VPXOR        Y1, Y0, Y0
	VMOVDQU      Y0, (DI)(BX*1)
	VPXOR        (R8)(BX*1), Y0, Y2
	VMOVDQU      Y2, (R8)(BX*1)
	VPXOR        (R9)(BX*1), Y0, Y3
	VMOVDQU      Y3, (R9)(BX*1)
	ADDQ         $4, SI
	ADDQ         $32, BX
	CMPQ         BX, CX
	JB           hdbloop
	VZEROUPPER
	RET

// SSSE3 tier (for CPUs without AVX2), 16 bytes per register. Legacy SSE
// instructions require aligned memory operands, so every load uses MOVOU.
// All kernels require n (or len(src)) to be a positive multiple of 16.

DATA nibbleMask<>+0(SB)/8, $0x0f0f0f0f0f0f0f0f
DATA nibbleMask<>+8(SB)/8, $0x0f0f0f0f0f0f0f0f
GLOBL nibbleMask<>(SB), RODATA|NOPTR, $16

DATA polyLow<>+0(SB)/8, $0x1d1d1d1d1d1d1d1d
DATA polyLow<>+8(SB)/8, $0x1d1d1d1d1d1d1d1d
GLOBL polyLow<>(SB), RODATA|NOPTR, $16

// func xorSSE2(dst, src []byte)
TEXT ·xorSSE2(SB), NOSPLIT, $0-48
	MOVQ dst_base+0(FP), DI
	MOVQ src_base+24(FP), SI
	MOVQ src_len+32(FP), CX
	SHRQ $4, CX

sx64:
	CMPQ  CX, $4
	JB    sx16
	MOVOU (SI), X0
	MOVOU 16(SI), X1
	MOVOU 32(SI), X2
	MOVOU 48(SI), X3
	MOVOU (DI), X4
	MOVOU 16(DI), X5
	MOVOU 32(DI), X6
	MOVOU 48(DI), X7
	PXOR  X4, X0
	PXOR  X5, X1
	PXOR  X6, X2
	PXOR  X7, X3
	MOVOU X0, (DI)
	MOVOU X1, 16(DI)
	MOVOU X2, 32(DI)
	MOVOU X3, 48(DI)
	ADDQ  $64, SI
	ADDQ  $64, DI
	SUBQ  $4, CX
	JMP   sx64

sx16:
	TESTQ CX, CX
	JZ    sxdone
	MOVOU (SI), X0
	MOVOU (DI), X4
	PXOR  X4, X0
	MOVOU X0, (DI)
	ADDQ  $16, SI
	ADDQ  $16, DI
	DECQ  CX
	JMP   sx16

sxdone:
	RET

// Nibble-table multiplication: c*x = lo[x & 15] ^ hi[x >> 4].

// func mulSSSE3(dst, src []byte, tbl *[32]byte)
TEXT ·mulSSSE3(SB), NOSPLIT, $0-56
	MOVQ  tbl+48(FP), AX
	MOVOU (AX), X6
	MOVOU 16(AX), X7
	MOVOU nibbleMask<>(SB), X8
	MOVQ  dst_base+0(FP), DI
	MOVQ  src_base+24(FP), SI
	MOVQ  src_len+32(FP), CX
	SHRQ  $4, CX

smloop:
	MOVOU  (SI), X0
	MOVOU  X0, X1
	PSRLQ  $4, X1
	PAND   X8, X0
	PAND   X8, X1
	MOVOU  X6, X2
	PSHUFB X0, X2
	MOVOU  X7, X3
	PSHUFB X1, X3
	PXOR   X3, X2
	MOVOU  X2, (DI)
	ADDQ   $16, SI
	ADDQ   $16, DI
	DECQ   CX
	JNZ    smloop
	RET

// func mulAddSSSE3(dst, src []byte, tbl *[32]byte)
TEXT ·mulAddSSSE3(SB), NOSPLIT, $0-56
	MOVQ  tbl+48(FP), AX
	MOVOU (AX), X6
	MOVOU 16(AX), X7
	MOVOU nibbleMask<>(SB), X8
	MOVQ  dst_base+0(FP), DI
	MOVQ  src_base+24(FP), SI
	MOVQ  src_len+32(FP), CX
	SHRQ  $4, CX

smaloop:
	MOVOU  (SI), X0
	MOVOU  X0, X1
	PSRLQ  $4, X1
	PAND   X8, X0
	PAND   X8, X1
	MOVOU  X6, X2
	PSHUFB X0, X2
	MOVOU  X7, X3
	PSHUFB X1, X3
	PXOR   X3, X2
	MOVOU  (DI), X4
	PXOR   X4, X2
	MOVOU  X2, (DI)
	ADDQ   $16, SI
	ADDQ   $16, DI
	DECQ   CX
	JNZ    smaloop
	RET

// Fused XOR of up to 8 sources, 32 bytes then 16 bytes at a time.

// func xorNSSE2(dst *byte, srcs *[8]*byte, nsrc int, n int, acc bool)
TEXT ·xorNSSE2(SB), NOSPLIT, $0-33
	MOVQ    dst+0(FP), DI
	MOVQ    srcs+8(FP), R8
	MOVQ    nsrc+16(FP), R9
	MOVQ    n+24(FP), CX
	MOVBLZX acc+32(FP), R10
	XORQ    AX, AX

sxn32:
	LEAQ  32(AX), DX
	CMPQ  DX, CX
	JA    sxn16
	TESTQ R10, R10
	JZ    sxn32first
	MOVOU (DI)(AX*1), X0
	MOVOU 16(DI)(AX*1), X1
	XORQ  BX, BX
	JMP   sxn32src

sxn32first:
	MOVQ  (R8), SI
	MOVOU (SI)(AX*1), X0
	MOVOU 16(SI)(AX*1), X1
	MOVQ  $1, BX

sxn32src:
	CMPQ  BX, R9
	JAE   sxn32store
	MOVQ  (R8)(BX*8), SI
	MOVOU (SI)(AX*1), X2
	MOVOU 16(SI)(AX*1), X3
	PXOR  X2, X0
	PXOR  X3, X1
	INCQ  BX
	JMP   sxn32src

sxn32store:
	MOVOU X0, (DI)(AX*1)
	MOVOU X1, 16(DI)(AX*1)
	MOVQ  DX, AX
	JMP   sxn32

sxn16:
	CMPQ  AX, CX
	JAE   sxndone
	TESTQ R10, R10
	JZ    sxn16first
	MOVOU (DI)(AX*1), X0
	XORQ  BX, BX
	JMP   sxn16src

sxn16first:
	MOVQ  (R8), SI
	MOVOU (SI)(AX*1), X0
	MOVQ  $1, BX

sxn16src:
	CMPQ  BX, R9
	JAE   sxn16store
	MOVQ  (R8)(BX*8), SI
	MOVOU (SI)(AX*1), X2
	PXOR  X2, X0
	INCQ  BX
	JMP   sxn16src

sxn16store:
	MOVOU X0, (DI)(AX*1)

sxndone:
	RET

// One step of the HDPC recurrence, 16 bytes at a time: z = alpha*z ^ y,
// h1 ^= z, h2 ^= z. PCMPGTB against zero selects the bytes whose high bit is
// set, which get 0x1D after doubling (PADDB). h1 and h2 may be the same.

// func hdpcStepSSE2(z, y, h1, h2 *byte, n int)
TEXT ·hdpcStepSSE2(SB), NOSPLIT, $0-40
	MOVQ  z+0(FP), DI
	MOVQ  y+8(FP), SI
	MOVQ  h1+16(FP), R8
	MOVQ  h2+24(FP), R9
	MOVQ  n+32(FP), CX
	MOVOU polyLow<>(SB), X7
	XORQ  BX, BX

shdloop:
	MOVOU   (DI)(BX*1), X0
	PXOR    X1, X1
	PCMPGTB X0, X1
	PADDB   X0, X0
	PAND    X7, X1
	PXOR    X1, X0
	MOVOU   (SI)(BX*1), X2
	PXOR    X2, X0
	MOVOU   X0, (DI)(BX*1)
	MOVOU   (R8)(BX*1), X3
	PXOR    X0, X3
	MOVOU   X3, (R8)(BX*1)
	MOVOU   (R9)(BX*1), X4
	PXOR    X0, X4
	MOVOU   X4, (R9)(BX*1)
	ADDQ    $16, BX
	CMPQ    BX, CX
	JB      shdloop
	RET
