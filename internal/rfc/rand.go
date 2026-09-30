package rfc

// Rand is the pseudo-random number generator Rand[y, i, m] of Section 5.3.5.1.
// It requires m > 0 and i < 256.
func Rand(y, i, m uint32) uint32 {
	x0 := (y + i) & 0xFF
	x1 := (y>>8 + i) & 0xFF
	x2 := (y>>16 + i) & 0xFF
	x3 := (y>>24 + i) & 0xFF
	return (v0[x0] ^ v1[x1] ^ v2[x2] ^ v3[x3]) % m
}

// Deg is the degree generator Deg[v] of Section 5.3.5.2 for v < 2^20: the
// index d with f[d-1] <= v < f[d], capped at W-2.
func (p *Params) Deg(v uint32) uint32 {
	d := uint32(1)
	for v >= degF[d] {
		d++
	}
	if w := uint32(p.W - 2); d > w {
		return w
	}
	return d
}

// Tuple is the output (d, a, b, d1, a1, b1) of the tuple generator.
type Tuple struct {
	D, A, B    uint32 // LT degree, step and start over the W LT symbols
	D1, A1, B1 uint32 // PI degree, step and start over the P PI symbols (walked mod P1)
}

// Tuple is the tuple generator Tuple[K', X] of Section 5.3.5.4 for the
// internal symbol ID x.
func (p *Params) Tuple(x uint32) Tuple {
	y := p.tupleB + x*p.tupleA // mod 2^32
	var t Tuple
	t.D = p.Deg(Rand(y, 0, 1<<20))
	t.A = 1 + Rand(y, 1, uint32(p.W-1))
	t.B = Rand(y, 2, uint32(p.W))
	if t.D < 4 {
		t.D1 = 2 + Rand(x, 3, 2)
	} else {
		t.D1 = 2
	}
	t.A1 = 1 + Rand(x, 4, uint32(p.P1-1))
	t.B1 = Rand(x, 5, uint32(p.P1))
	return t
}
