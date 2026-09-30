//go:build !purego

package cpu

func cpuid(eaxArg, ecxArg uint32) (eax, ebx, ecx, edx uint32)

func xgetbv() (eax, edx uint32)

func init() {
	maxID, _, _, _ := cpuid(0, 0)
	if maxID < 7 {
		return
	}
	_, _, ecx1, _ := cpuid(1, 0)
	osxsave := ecx1&(1<<27) != 0
	avx := ecx1&(1<<28) != 0
	if !osxsave || !avx {
		return
	}
	// The OS must save and restore the XMM and YMM registers.
	if xcr0, _ := xgetbv(); xcr0&6 != 6 {
		return
	}
	_, ebx7, ecx7, _ := cpuid(7, 0)
	X86.HasAVX2 = ebx7&(1<<5) != 0
	X86.HasGFNI = ecx7&(1<<8) != 0
}
