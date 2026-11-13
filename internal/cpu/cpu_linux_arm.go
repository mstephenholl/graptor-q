//go:build !purego

package cpu

import _ "unsafe" // for go:linkname

// runtime/runtime.go keeps getAuxv for golang.org/x/sys/cpu: "Do not remove or change the type signature".
//
//go:linkname runtime_getAuxv runtime.getAuxv
func runtime_getAuxv() []uintptr

const (
	_AT_HWCAP   = 16      // include/uapi/linux/auxvec.h
	_HWCAP_NEON = 1 << 12 // arch/arm/include/uapi/asm/hwcap.h
)

func init() {
	auxv := runtime_getAuxv()
	for i := 0; i+1 < len(auxv); i += 2 {
		if auxv[i] == _AT_HWCAP {
			ARM.HasNEON = auxv[i+1]&_HWCAP_NEON != 0
		}
	}
}
