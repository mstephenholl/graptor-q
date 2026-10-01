//go:build purego || !(amd64 || arm64)

package gf256

func archTiers() []*kernels { return nil }

func xorN(dst []byte, srcs [][]byte, acc bool) { xorNGeneric(dst, srcs, acc) }

func hdpcStep(z, y, h1, h2 []byte) { hdpcStepGeneric(z, y, h1, h2) }

func hdpcStepBits(z []byte, x []uint64, h1, h2 []byte) { hdpcStepBitsSpread(z, x, 0, h1, h2) }

func xorGather(dst, first, base []byte, stride int, idx []uint16, acc bool) {
	xorGatherGeneric(dst, first, base, stride, idx, acc)
}
