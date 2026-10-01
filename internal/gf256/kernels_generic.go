package gf256

import "encoding/binary"

var genericKernels = kernels{
	name:   "generic",
	xor:    xorGeneric,
	mul:    mulGeneric,
	mulAdd: mulAddGeneric,
}

// xorNGeneric sets dst = srcs[0] ^ ... ^ srcs[n-1] (or dst ^= ... when acc)
// for 1 <= n <= 8 sources. It works in chunks small enough to stay in L1
// cache, so dst is
// read and written from memory only once.
func xorNGeneric(dst []byte, srcs [][]byte, acc bool) {
	const chunk = 512
	for off := 0; off < len(dst); off += chunk {
		end := min(off+chunk, len(dst))
		d := dst[off:end]
		i := 0
		if !acc {
			copy(d, srcs[0][off:end])
			i = 1
		}
		for ; i < len(srcs); i++ {
			xorGeneric(d, srcs[i][off:end])
		}
	}
}

// hdpcStepGeneric: z = alpha*z ^ y; h1 ^= z; h2 ^= z. Multiplication by
// alpha doubles each byte and reduces by the polynomial where the high bit
// was set, 8 bytes at a time.
func hdpcStepGeneric(z, y, h1, h2 []byte) {
	y, h1, h2 = y[:len(z)], h1[:len(z)], h2[:len(z)]
	n := len(z) &^ 7
	for i := 0; i < n; i += 8 {
		x := binary.LittleEndian.Uint64(z[i:])
		hi := x & 0x8080808080808080
		x = (x&^hi)<<1 ^ (hi>>7)*(Poly&0xFF) ^ binary.LittleEndian.Uint64(y[i:])
		binary.LittleEndian.PutUint64(z[i:], x)
		binary.LittleEndian.PutUint64(h1[i:], binary.LittleEndian.Uint64(h1[i:])^x)
		binary.LittleEndian.PutUint64(h2[i:], binary.LittleEndian.Uint64(h2[i:])^x)
	}
	for i := n; i < len(z); i++ {
		b := z[i]<<1 ^ z[i]>>7*(Poly&0xFF) ^ y[i]
		z[i] = b
		h1[i] ^= b
		h2[i] ^= b
	}
}

// spreadBits[b] has byte i equal to bit i of b.
var spreadBits = func() (t [256]uint64) {
	for b := range t {
		for i := range 8 {
			t[b] |= uint64(b>>i&1) << (8 * i)
		}
	}
	return
}()

// hdpcStepBitsSpread is hdpcStepBits for any tier: it expands the bits of x
// from bit off (a multiple of 8) into bytes, a chunk at a time, and runs
// hdpcStep on each chunk.
func hdpcStepBitsSpread(z []byte, x []uint64, off int, h1, h2 []byte) {
	var y [256]byte
	for i := 0; i < len(z); i += len(y) {
		n := min(len(y), len(z)-i)
		for k := 0; k < n; k += 8 {
			b := (off + i + k) >> 3 // byte of the bitset
			binary.LittleEndian.PutUint64(y[k:], spreadBits[byte(x[b>>3]>>(8*(b&7)))])
		}
		hdpcStep(z[i:i+n], y[:n], h1[i:i+n], h2[i:i+n])
	}
}

func xorGeneric(dst, src []byte) {
	dst = dst[:len(src)]
	for len(src) >= 32 {
		d, s := dst[:32:32], src[:32:32]
		binary.LittleEndian.PutUint64(d[0:], binary.LittleEndian.Uint64(d[0:])^binary.LittleEndian.Uint64(s[0:]))
		binary.LittleEndian.PutUint64(d[8:], binary.LittleEndian.Uint64(d[8:])^binary.LittleEndian.Uint64(s[8:]))
		binary.LittleEndian.PutUint64(d[16:], binary.LittleEndian.Uint64(d[16:])^binary.LittleEndian.Uint64(s[16:]))
		binary.LittleEndian.PutUint64(d[24:], binary.LittleEndian.Uint64(d[24:])^binary.LittleEndian.Uint64(s[24:]))
		dst, src = dst[32:], src[32:]
	}
	for len(src) >= 8 {
		binary.LittleEndian.PutUint64(dst, binary.LittleEndian.Uint64(dst)^binary.LittleEndian.Uint64(src))
		dst, src = dst[8:], src[8:]
	}
	for i, s := range src {
		dst[i] ^= s
	}
}

func mulGeneric(dst, src []byte, c byte) {
	t := &mulTable[c]
	dst = dst[:len(src)]
	for i, s := range src {
		dst[i] = t[s]
	}
}

func mulAddGeneric(dst, src []byte, c byte) {
	t := &mulTable[c]
	dst = dst[:len(src)]
	for len(src) >= 8 {
		s := binary.LittleEndian.Uint64(src)
		m := uint64(t[byte(s)]) | uint64(t[byte(s>>8)])<<8 | uint64(t[byte(s>>16)])<<16 | uint64(t[byte(s>>24)])<<24 |
			uint64(t[byte(s>>32)])<<32 | uint64(t[byte(s>>40)])<<40 | uint64(t[byte(s>>48)])<<48 | uint64(t[byte(s>>56)])<<56
		binary.LittleEndian.PutUint64(dst, binary.LittleEndian.Uint64(dst)^m)
		dst, src = dst[8:], src[8:]
	}
	for i, s := range src {
		dst[i] ^= t[s]
	}
}
