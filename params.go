package graptorq

import (
	"fmt"

	"github.com/mstephenholl/graptor-q/internal/rfc"
)

// Partition is the function Partition[I, J] of RFC 6330 Section 4.4.1.2: it
// splits I into JL parts of size IL and JS parts of size IS.
func Partition(I, J int) (IL, IS, JL, JS int) {
	IL = (I + J - 1) / J
	IS = I / J
	JL = I - IS*J
	JS = J - JL
	return
}

// Config holds the inputs of the parameter derivation algorithm of RFC 6330
// Section 4.3.
type Config struct {
	// PayloadSize is P', the maximum payload size in bytes: the symbol
	// size T. It must be a multiple of Alignment.
	PayloadSize int
	// Alignment is Al, the symbol alignment in bytes. Zero means 4, the
	// value recommended by the RFC.
	Alignment int
	// MinSubSymbolSize is SS: sub-symbols are at least SS*Al bytes. Zero
	// means 1.
	MinSubSymbolSize int
	// WorkingMemory is WS, the maximum size in bytes of a sub-block that a
	// receiver can decode in working memory. Zero means 16 MiB.
	WorkingMemory int64
}

// DeriveOTI computes the OTI for an object of the given transfer length
// following RFC 6330 Section 4.3:
//
//	T = P', Kt = ceil(F/T), N_max = floor(T/(SS*Al)),
//	KL(n) = max K' in Table 2 with K' <= WS/(Al*ceil(T/(Al*n))),
//	Z = ceil(Kt/KL(N_max)), N = min n with ceil(Kt/Z) <= KL(n).
func DeriveOTI(transferLength uint64, c Config) (OTI, error) {
	al := c.Alignment
	if al == 0 {
		al = 4
	}
	ss := c.MinSubSymbolSize
	if ss == 0 {
		ss = 1
	}
	ws := c.WorkingMemory
	if ws == 0 {
		ws = 16 << 20
	}
	T := c.PayloadSize
	switch {
	case al < 1 || al > 255:
		return OTI{}, &ParamError{"alignment", uint64(max(al, 0)), "must be in [1, 255]"}
	case T < 1 || T > maxSymbolSize || T%al != 0:
		return OTI{}, &ParamError{"payload size", uint64(max(T, 0)), fmt.Sprintf("must be a positive multiple of %d below 65536", al)}
	case ss < 1 || T < ss*al:
		return OTI{}, &ParamError{"minimum sub-symbol size", uint64(max(ss, 0)), "SS*Al exceeds the symbol size"}
	case transferLength == 0 || transferLength > MaxTransferLength:
		return OTI{}, &ParamError{"transfer length", transferLength, "out of range"}
	case ws < 1:
		return OTI{}, &ParamError{"working memory", 0, "must be positive"}
	}

	kt := ceilDiv(transferLength, uint64(T))
	nMax := T / (ss * al)
	kl := func(n int) int { // 0 if even K' = 10 does not fit
		bound := ws / int64(al*((T+al*n-1)/(al*n)))
		best := 0
		for _, p := range rfc.All() {
			if int64(p.KPrime) > bound {
				break
			}
			best = p.KPrime
		}
		return best
	}
	klMax := kl(nMax)
	if klMax == 0 {
		return OTI{}, &ParamError{"working memory", uint64(ws), "too small for the smallest source block"}
	}
	z := ceilDiv(kt, uint64(klMax))
	if z > MaxSourceBlocks {
		return OTI{}, &ParamError{"transfer length", transferLength, fmt.Sprintf("needs %d source blocks with this working memory", z)}
	}
	perBlock := ceilDiv(kt, z)
	n := nMax
	for i := 1; i <= nMax; i++ {
		if perBlock <= uint64(kl(i)) {
			n = i
			break
		}
	}
	oti := OTI{
		TransferLength: transferLength,
		SymbolSize:     uint16(T),
		SourceBlocks:   uint8(z),
		SubBlocks:      uint16(n),
		Alignment:      uint8(al),
	}
	return oti, oti.Validate()
}
