package rfc

import (
	"os"
	"testing"

	"github.com/mholland/graptorq/internal/rfc/rfctext"
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
	if _, err := ForK(56404); err != ErrTooManySymbols {
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
