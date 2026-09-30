package rfc

// Rand is the pseudo-random number generator Rand[y, i, m] of Section 5.3.5.1.
// It requires m > 0 and i < 256.
func Rand(y, i, m uint32) uint32 { return rawRand(y, i) % m }

// rawRand is Rand before the final reduction modulo m.
func rawRand(y, i uint32) uint32 {
	return v0[byte(y+i)] ^ v1[byte(y>>8+i)] ^ v2[byte(y>>16+i)] ^ v3[byte(y>>24+i)]
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
	t.D = p.Deg(rawRand(y, 0) & (1<<20 - 1)) // Rand[y, 0, 2^20]
	t.A = 1 + p.modW1.mod(rawRand(y, 1))     // 1 + Rand[y, 1, W-1]
	t.B = p.modW.mod(rawRand(y, 2))          // Rand[y, 2, W]
	if t.D < 4 {
		t.D1 = 2 + rawRand(x, 3)&1 // 2 + Rand[X, 3, 2]
	} else {
		t.D1 = 2
	}
	t.A1 = 1 + p.modP11.mod(rawRand(x, 4)) // 1 + Rand[X, 4, P1-1]
	t.B1 = p.modP1.mod(rawRand(x, 5))      // Rand[X, 5, P1]
	return t
}
