package rfctext

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"testing"
)

// RFCPath is the location of the committed RFC text relative to this package.
const rfcPath = "../../../testdata/rfc6330.txt"

// SHA-256 of https://www.rfc-editor.org/rfc/rfc6330.txt as committed.
const rfcSHA256 = "87f6da89cc325987cb2910d1c124f74eaeac5c86a83f962cf347c0cc66321ace"

func load(t *testing.T) *Tables {
	t.Helper()
	text, err := os.ReadFile(rfcPath)
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(text); hex.EncodeToString(sum[:]) != rfcSHA256 {
		t.Fatalf("testdata/rfc6330.txt SHA-256 = %x, want %s", sum, rfcSHA256)
	}
	tables, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return tables
}

func TestVTables(t *testing.T) {
	tables := load(t)
	// First/last entries, sum mod 2^32 and XOR of all entries, computed
	// independently from the RFC text during planning.
	want := [4]struct{ first, last, sum, xor uint32 }{
		{251291136, 1358307511, 331395769, 2200914733},
		{807385413, 4135048896, 3270332586, 3069643552},
		{1629829892, 3497665928, 1176160967, 620986173},
		{1191369816, 3432275192, 3374522247, 1772968547},
	}
	for i, v := range tables.V {
		var sum, xor uint32
		for _, x := range v {
			sum += x
			xor ^= x
		}
		w := want[i]
		if v[0] != w.first || v[255] != w.last || sum != w.sum || xor != w.xor {
			t.Errorf("V%d: first=%d last=%d sum=%d xor=%d, want %+v", i, v[0], v[255], sum, xor, w)
		}
	}
}

func TestDegreeTable(t *testing.T) {
	tables := load(t)
	f := tables.DegF
	if f[0] != 0 || f[1] != 5243 || f[2] != 529531 || f[29] != 1017662 || f[30] != 1<<20 {
		t.Fatalf("unexpected Table 1: %v", f)
	}
	for d := 1; d < len(f); d++ {
		if f[d] <= f[d-1] {
			t.Fatalf("Table 1 not strictly increasing at d=%d", d)
		}
	}
}

func TestSystematicTable(t *testing.T) {
	tables := load(t)
	rows := tables.Systematic
	if first := rows[0]; first != (SystematicRow{10, 254, 7, 10, 17}) {
		t.Errorf("first row = %+v", first)
	}
	if last := rows[len(rows)-1]; last != (SystematicRow{56403, 471, 907, 16, 56951}) {
		t.Errorf("last row = %+v", last)
	}
	// Canonical digest (5 x uint32 big-endian per row), matching the value
	// published by github.com/takeyourhatoff/raptorq, which was checked against
	// cberner/raptorq 2.0.1's copy of the table.
	h := sha256.New()
	var buf [20]byte
	for _, r := range rows {
		for i, v := range []int{r.KPrime, r.J, r.S, r.H, r.W} {
			binary.BigEndian.PutUint32(buf[i*4:], uint32(v))
		}
		h.Write(buf[:])
	}
	const want = "9d3657cf3255cf51f9a93b762a37fbe740598e3b14259acc5641abedcd0da7d6"
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		t.Errorf("Table 2 digest = %s, want %s", got, want)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].KPrime <= rows[i-1].KPrime {
			t.Fatalf("K' not increasing at row %d", i)
		}
	}
}

func TestOctTables(t *testing.T) {
	tables := load(t)
	if tables.OctExp[0] != 1 || tables.OctExp[8] != 29 || tables.OctExp[509] != 142 {
		t.Errorf("unexpected OCT_EXP endpoints")
	}
	if tables.OctLog[1] != 0 || tables.OctLog[2] != 1 || tables.OctLog[255] != 175 {
		t.Errorf("unexpected OCT_LOG endpoints")
	}
	for v := 1; v < 256; v++ {
		if int(tables.OctExp[tables.OctLog[v]]) != v {
			t.Fatalf("OCT_EXP[OCT_LOG[%d]] != %d", v, v)
		}
	}
}
