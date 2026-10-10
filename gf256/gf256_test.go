package gf256_test

import (
	"bytes"
	"math/rand/v2"
	"testing"

	"github.com/mstephenholl/graptor-q/gf256"
)

// TestSliceKernelsAgreeWithMul checks every tier's slice kernels against the
// scalar field for every coefficient, at lengths around the kernels' vector
// widths.
func TestSliceKernelsAgreeWithMul(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for _, tier := range gf256.Tiers() {
		restore := gf256.Use(tier)
		for _, n := range []int{0, 1, 15, 16, 17, 31, 32, 33, 63, 64, 65, 1400, 1408} {
			src, dst := make([]byte, n), make([]byte, n)
			for i := range src {
				src[i], dst[i] = byte(r.Uint32()), byte(r.Uint32())
			}
			for c := range 256 {
				want := make([]byte, n)
				for i := range want {
					want[i] = dst[i] ^ gf256.Mul(byte(c), src[i])
				}
				got := bytes.Clone(dst)
				gf256.MulAddSlice(got, src, byte(c))
				if !bytes.Equal(got, want) {
					t.Fatalf("tier %s: MulAddSlice(n=%d, c=%d) disagrees with Mul", tier, n, c)
				}
				gf256.MulSlice(got, src, byte(c))
				for i := range want {
					want[i] = gf256.Mul(byte(c), src[i])
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("tier %s: MulSlice(n=%d, c=%d) disagrees with Mul", tier, n, c)
				}
			}
			got := bytes.Clone(dst)
			gf256.AddSlice(got, src)
			for i := range got {
				if got[i] != dst[i]^src[i] {
					t.Fatalf("tier %s: AddSlice(n=%d) is not xor at %d", tier, n, i)
				}
			}
		}
		restore()
	}
}

func TestFieldIdentities(t *testing.T) {
	if gf256.Poly != 0x11D {
		t.Fatalf("Poly = %#x, want 0x11D", gf256.Poly)
	}
	if gf256.Exp(8) != 0x1D {
		t.Errorf("alpha^8 = %#x, want 0x1d (x^8 reduced by 0x11D)", gf256.Exp(8))
	}
	for a := 1; a < 256; a++ {
		if gf256.Mul(byte(a), gf256.Inv(byte(a))) != 1 {
			t.Fatalf("%d * Inv(%d) != 1", a, a)
		}
		if gf256.Exp(gf256.Log(byte(a))) != byte(a) {
			t.Fatalf("Exp(Log(%d)) != %d", a, a)
		}
		if gf256.Div(gf256.Mul(byte(a), 7), 7) != byte(a) {
			t.Fatalf("Div(Mul(%d, 7), 7) != %d", a, a)
		}
	}
}
