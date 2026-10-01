package rfc

import (
	"errors"
	"math/rand/v2"
	"os"
	"slices"
	"testing"

	"github.com/mstephenholl/graptor-q/internal/rfc/rfctext"
)

// The generated tables must be exactly what the RFC text says.
func TestGeneratedTablesMatchRFC(t *testing.T) {
	text, err := os.ReadFile("../../testdata/rfc6330.txt")
	if err != nil {
		t.Fatal(err)
	}
	want, err := rfctext.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if want.V[0] != v0 || want.V[1] != v1 || want.V[2] != v2 || want.V[3] != v3 {
		t.Error("V tables differ from RFC text; run go generate")
	}
	if want.DegF != degF {
		t.Error("degree table differs from RFC text; run go generate")
	}
	if len(want.Systematic) != len(systematic) {
		t.Fatalf("Table 2 has %d rows, RFC has %d", len(systematic), len(want.Systematic))
	}
	for i, r := range want.Systematic {
		g := systematic[i]
		got := rfctext.SystematicRow{KPrime: int(g.kPrime), J: int(g.j), S: int(g.s), H: int(g.h), W: int(g.w)}
		if got != r {
			t.Fatalf("Table 2 row %d = %+v, RFC has %+v", i, g, r)
		}
	}
}

func TestParamsInvariants(t *testing.T) {
	for _, p := range All() {
		if !isPrime(p.S) || !isPrime(p.W) || !isPrime(p.P1) {
			t.Fatalf("K'=%d: S=%d W=%d P1=%d must all be prime", p.KPrime, p.S, p.W, p.P1)
		}
		if p.P1 < p.P || (p.P1 > p.P && isPrime(p.P)) {
			t.Fatalf("K'=%d: P1=%d is not the smallest prime >= P=%d", p.KPrime, p.P1, p.P)
		}
		if p.P < p.H || p.U < 0 || p.B <= 0 || p.W >= p.L {
			t.Fatalf("K'=%d: inconsistent params %+v", p.KPrime, p)
		}
		if p.KPrime <= 48 && p.U != 0 {
			t.Fatalf("K'=%d: U=%d, want 0", p.KPrime, p.U)
		}
		if p.L > 1<<16 {
			t.Fatalf("K'=%d: L=%d does not fit uint16 column indices", p.KPrime, p.L)
		}
	}
	last := All()[len(All())-1]
	if last.L != 57326 || last.P != 375 || last.P1 != 379 || last.U != 359 || last.B != 56044 {
		t.Fatalf("K'_max params = %+v", last)
	}
}

func TestForK(t *testing.T) {
	for _, c := range []struct{ k, want int }{
		{1, 10}, {10, 10}, {11, 12}, {49, 49}, {50, 55}, {1020, 1020}, {1021, 1032}, {56403, 56403},
	} {
		p, err := ForK(c.k)
		if err != nil || p.KPrime != c.want {
			t.Errorf("ForK(%d) = %v, %v; want K'=%d", c.k, p, err, c.want)
		}
	}
	if _, err := ForK(56404); !errors.Is(err, ErrTooManySymbols) {
		t.Errorf("ForK(56404) error = %v", err)
	}
	if _, err := ForK(0); err == nil {
		t.Error("ForK(0) succeeded")
	}
}

func TestRandKnownAnswer(t *testing.T) {
	if got := Rand(1234567, 3, 1000); got != 54 {
		t.Errorf("Rand[1234567,3,1000] = %d, want 54", got)
	}
	if got := v0[0] ^ v1[0] ^ v2[0] ^ v3[0]; got != 415381529 {
		t.Errorf("V0[0]^V1[0]^V2[0]^V3[0] = %d, want 415381529", got)
	}
}

func TestTupleKnownAnswer(t *testing.T) {
	// Computed independently from the RFC pseudo-code during planning.
	p, _ := ForK(10)
	for _, c := range []struct {
		x    uint32
		want Tuple
	}{
		{0, Tuple{2, 4, 9, 2, 5, 1}},
		{1, Tuple{7, 6, 12, 2, 1, 3}},
		{2, Tuple{15, 6, 3, 2, 1, 0}},
		{3, Tuple{2, 1, 4, 3, 3, 8}},
		{10, Tuple{2, 15, 15, 2, 10, 7}},
	} {
		if got := p.Tuple(c.x); got != c.want {
			t.Errorf("Tuple[10, %d] = %v, want %v", c.x, got, c.want)
		}
	}
}

// The optimized Tuple and column walk must equal the literal pseudo-code of
// Sections 5.3.5.3 and 5.3.5.4 for every K' and many ISIs, including the
// fastmod reductions at the extremes of the 32-bit range.
func TestTupleMatchesRFCFormula(t *testing.T) {
	rng := rand.New(rand.NewPCG(81, 82))
	for i := range All() {
		p := &All()[i]
		for n := range 300 {
			x := rng.Uint32()
			switch n {
			case 0:
				x = 0
			case 1:
				x = 1<<32 - 1
			}
			a := uint32(53591 + p.J*997)
			if a%2 == 0 {
				a++
			}
			y := uint32(10267*(p.J+1)) + x*a
			d := p.Deg(Rand(y, 0, 1<<20))
			want := Tuple{D: d, A: 1 + Rand(y, 1, uint32(p.W-1)), B: Rand(y, 2, uint32(p.W)), D1: 2}
			if d < 4 {
				want.D1 = 2 + Rand(x, 3, 2)
			}
			want.A1 = 1 + Rand(x, 4, uint32(p.P1-1))
			want.B1 = Rand(x, 5, uint32(p.P1))
			if got := p.Tuple(x); got != want {
				t.Fatalf("K'=%d X=%d: Tuple = %v, want %v", p.KPrime, x, got, want)
			}
			// Literal Enc walk.
			var cols []uint16
			b := want.B
			cols = append(cols, uint16(b))
			for j := uint32(1); j <= want.D-1; j++ {
				b = (b + want.A) % uint32(p.W)
				cols = append(cols, uint16(b))
			}
			b1 := want.B1
			for b1 >= uint32(p.P) {
				b1 = (b1 + want.A1) % uint32(p.P1)
			}
			cols = append(cols, uint16(uint32(p.W)+b1))
			for j := uint32(1); j <= want.D1-1; j++ {
				b1 = (b1 + want.A1) % uint32(p.P1)
				for b1 >= uint32(p.P) {
					b1 = (b1 + want.A1) % uint32(p.P1)
				}
				cols = append(cols, uint16(uint32(p.W)+b1))
			}
			if got := p.AppendTupleCols(nil, want); !slices.Equal(got, cols) {
				t.Fatalf("K'=%d X=%d: columns %v, want %v", p.KPrime, x, got, cols)
			}
		}
	}
}

func TestModulus(t *testing.T) {
	rng := rand.New(rand.NewPCG(83, 84))
	for _, d := range []uint32{1, 2, 3, 7, 16, 1<<20 - 1, 56951, 1<<31 + 11, 1<<32 - 1} {
		m := newModulus(d)
		for range 10000 {
			x := rng.Uint32()
			if got := m.mod(x); got != x%d {
				t.Fatalf("%d mod %d = %d, want %d", x, d, got, x%d)
			}
		}
		for _, x := range []uint32{0, 1, d - 1, d, 1<<32 - 1} {
			if got := m.mod(x); got != x%d {
				t.Fatalf("%d mod %d = %d, want %d", x, d, got, x%d)
			}
		}
	}
}

func TestDegBoundaries(t *testing.T) {
	p, _ := ForK(56403) // W-2 does not cap
	for d := 1; d < len(degF); d++ {
		if got := p.Deg(degF[d-1]); got != uint32(d) {
			t.Fatalf("Deg(f[%d]) = %d, want %d", d-1, got, d)
		}
		if got := p.Deg(degF[d] - 1); got != uint32(d) {
			t.Fatalf("Deg(f[%d]-1) = %d, want %d", d, got, d)
		}
	}
	small, _ := ForK(10) // W = 17, so Deg is capped at 15
	if got := small.Deg(1<<20 - 1); got != 15 {
		t.Fatalf("Deg capped = %d, want 15", got)
	}
}

func TestEncColsDistinct(t *testing.T) {
	var cols []uint16
	for _, p := range All() {
		for _, x := range []uint32{0, 1, uint32(p.KPrime - 1), uint32(p.KPrime), 1<<24 - 1, 1<<24 + 56402} {
			cols = p.AppendEncCols(cols[:0], x)
			tu := p.Tuple(x)
			if len(cols) != int(tu.D+tu.D1) {
				t.Fatalf("K'=%d x=%d: %d cols, want %d", p.KPrime, x, len(cols), tu.D+tu.D1)
			}
			seen := map[uint16]bool{}
			for i, c := range cols {
				if seen[c] {
					t.Fatalf("K'=%d x=%d: duplicate column %d", p.KPrime, x, c)
				}
				seen[c] = true
				lt := i < int(tu.D)
				if lt && int(c) >= p.W || !lt && (int(c) < p.W || int(c) >= p.L) {
					t.Fatalf("K'=%d x=%d: column %d out of range", p.KPrime, x, c)
				}
			}
		}
	}
}

// The columns of every LT row are distinct, for any ISI (so rows need no
// cancellation of repeated entries):
//   - LT part: b + i*a mod W for i < d, with W prime, 0 < a < W and d <= W-2.
//   - PI part: the walk b1 + i*a1 mod P1 has period P1 (prime, 0 < a1 < P1);
//     each of the d1 <= 3 accepted values costs at most 1 + (P1-P) steps
//     (values >= P are skipped), so they are distinct if 3*(1+P1-P) < P1.
func TestEncColsDistinctProof(t *testing.T) {
	for _, p := range All() {
		if p.W-2 >= p.W || !isPrime(p.W) || !isPrime(p.P1) {
			t.Fatalf("K'=%d: LT argument does not apply", p.KPrime)
		}
		if 3*(1+p.P1-p.P) >= p.P1 {
			t.Fatalf("K'=%d: P=%d P1=%d: PI walk could repeat within 3 values", p.KPrime, p.P, p.P1)
		}
	}
}

func TestLDPCRows(t *testing.T) {
	for _, p := range All() {
		rows := p.LDPCRows()
		total := 0
		for i, r := range rows {
			total += len(r)
			has := false
			for _, c := range r {
				if int(c) == p.B+i {
					has = true
				}
				if int(c) >= p.B && int(c) < p.W && int(c) != p.B+i {
					t.Fatalf("K'=%d row %d: stray identity column %d", p.KPrime, i, c)
				}
			}
			if !has {
				t.Fatalf("K'=%d row %d: missing identity column", p.KPrime, i)
			}
		}
		// No cancellation happens for any Table 2 entry: 3 entries per LT
		// column, one identity and two PI entries per row.
		if want := 3*p.B + 3*p.S; total != want {
			t.Fatalf("K'=%d: %d LDPC entries, want %d", p.KPrime, total, want)
		}
	}
}

func TestHDPCPairsDistinct(t *testing.T) {
	for _, p := range All() {
		r1, r2 := p.HDPCPairs()
		for j := range r1 {
			if r1[j] == r2[j] || int(r1[j]) >= p.H || int(r2[j]) >= p.H {
				t.Fatalf("K'=%d j=%d: bad pair (%d,%d)", p.KPrime, j, r1[j], r2[j])
			}
		}
	}
}
