package gf256

import "encoding/binary"

var genericKernels = kernels{
	name:   "generic",
	xor:    xorGeneric,
	mul:    mulGeneric,
	mulAdd: mulAddGeneric,
	xorN:   xorNGeneric,
}

// xorNGeneric works in chunks small enough to stay in L1 cache, so dst is
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
