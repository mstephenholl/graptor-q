// Package gf256 implements arithmetic in GF(2^8) as used by RFC 6330
// (Section 5.7), together with slice kernels over symbols.
//
// The field is defined by the polynomial x^8 + x^4 + x^3 + x^2 + 1 (0x11D) with
// generator alpha = 2; this is the field whose OCT_EXP and OCT_LOG tables are
// given in RFC 6330 Sections 5.7.3 and 5.7.4.
package gf256

// Poly is the reduction polynomial of the field.
const Poly = 0x11D

var (
	// expTable[i] = alpha^i for 0 <= i < 510, so exp[log a + log b] needs no
	// reduction (the same layout as OCT_EXP).
	expTable [510]byte
	// logTable[a] = log_alpha(a) for a != 0 (OCT_LOG); logTable[0] is unused.
	logTable [256]byte
	invTable [256]byte
	// mulTable[a][b] = a * b.
	mulTable [256][256]byte
	// nibbleTables[c] holds c*x for x in [0,16) followed by c*(x<<4) for x in
	// [0,16): the two lookup tables of the PSHUFB/TBL multiplication method.
	nibbleTables [256][32]byte
	// gfniMatrix[c] is the 8x8 bit matrix of multiplication by c in the byte
	// layout expected by the x86 GF2P8AFFINEQB instruction.
	gfniMatrix [256]uint64
)

func init() {
	x := 1
	for i := range 255 {
		expTable[i] = byte(x)
		expTable[i+255] = byte(x)
		logTable[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= Poly
		}
	}
	for a := 1; a < 256; a++ {
		invTable[a] = expTable[255-int(logTable[a])]
		la := int(logTable[a])
		for b := 1; b < 256; b++ {
			mulTable[a][b] = expTable[la+int(logTable[b])]
		}
	}
	for c := range 256 {
		for i := range 16 {
			nibbleTables[c][i] = mulTable[c][i]
			nibbleTables[c][16+i] = mulTable[c][i<<4]
		}
		gfniMatrix[c] = affineMatrix(byte(c))
	}
}

// affineMatrix returns the GF2P8AFFINEQB operand A such that the instruction
// computes c*x for every byte x. For output bit i the instruction takes the
// parity of x AND A.byte[7-i]; bit j of that byte is therefore bit i of c*2^j.
func affineMatrix(c byte) uint64 {
	var m uint64
	for i := range 8 {
		var row byte
		for j := range 8 {
			if mulTable[c][1<<j]>>i&1 != 0 {
				row |= 1 << j
			}
		}
		m |= uint64(row) << (8 * (7 - i))
	}
	return m
}

// Mul returns a * b.
func Mul(a, b byte) byte { return mulTable[a][b] }

// Div returns a / b. It panics if b is zero.
func Div(a, b byte) byte {
	if b == 0 {
		panic("gf256: division by zero")
	}
	return mulTable[a][invTable[b]]
}

// Inv returns the multiplicative inverse of a. It panics if a is zero.
func Inv(a byte) byte {
	if a == 0 {
		panic("gf256: inverse of zero")
	}
	return invTable[a]
}

// Exp returns alpha^n for n >= 0.
func Exp(n int) byte { return expTable[n%255] }

// Log returns log_alpha(a) in [0, 255). It panics if a is zero.
func Log(a byte) int {
	if a == 0 {
		panic("gf256: log of zero")
	}
	return int(logTable[a])
}

// MulTable returns the row of the multiplication table for c: MulTable(c)[x] = c*x.
func MulTable(c byte) *[256]byte { return &mulTable[c] }
