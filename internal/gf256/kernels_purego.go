//go:build purego || !(amd64 || arm64)

package gf256

func archTiers() []*kernels { return nil }
