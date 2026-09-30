package graptorq

import (
	"slices"
	"testing"

	"github.com/mholland/graptorq/internal/rfc"
	"github.com/mholland/graptorq/internal/solver"
)

// lruOrder returns the K' of the plans in c, most recently used first. It
// checks that the list is linked consistently in both directions and agrees
// with the entries map and the byte count.
func lruOrder(t *testing.T, c *PlanCache) []int {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var order []int
	var bytes int64
	var prev *planEntry
	for e := c.front; e != nil; e = e.next {
		if e.prev != prev || !e.listed || c.entries[e.kPrime] != e {
			t.Fatalf("LRU list is inconsistent at K'=%d", e.kPrime)
		}
		order = append(order, e.kPrime)
		bytes += e.size
		prev = e
	}
	if c.back != prev {
		t.Fatal("LRU list: back is not the last entry")
	}
	if bytes != c.bytes || len(order) != len(c.entries) {
		t.Fatalf("LRU list holds %d plans (%d bytes), the cache counts %d (%d bytes)",
			len(order), bytes, len(c.entries), c.bytes)
	}
	return order
}

// The plan cache moves used plans to the front and drops the least recently
// used plans first, but always keeps the newest one.
func TestPlanCacheLRU(t *testing.T) {
	var ps []*rfc.Params
	var sizes []int64
	for _, k := range []int{10, 100, 1000} {
		p, err := rfc.ForK(k)
		if err != nil {
			t.Fatal(err)
		}
		pl, err := solver.NewPlan(p, seqISIs(p.KPrime))
		if err != nil {
			t.Fatal(err)
		}
		ps = append(ps, p)
		sizes = append(sizes, int64(pl.Size()))
	}
	a, b, c := ps[0], ps[1], ps[2]
	get := func(cache *PlanCache, p *rfc.Params) *solver.Plan {
		t.Helper()
		pl, err := cache.plan(p)
		if err != nil {
			t.Fatal(err)
		}
		return pl
	}
	want := func(cache *PlanCache, order ...*rfc.Params) {
		t.Helper()
		var kps []int
		for _, p := range order {
			kps = append(kps, p.KPrime)
		}
		if got := lruOrder(t, cache); !slices.Equal(got, kps) {
			t.Fatalf("LRU order %v, want %v", got, kps)
		}
	}

	// A used plan moves to the front from any position.
	cache := NewPlanCache(1 << 30)
	first := get(cache, a)
	get(cache, b)
	get(cache, c)
	want(cache, c, b, a)
	get(cache, b) // middle
	want(cache, b, c, a)
	if get(cache, a) != first { // back
		t.Fatal("cached plan was recomputed")
	}
	want(cache, a, b, c)
	get(cache, a) // front
	want(cache, a, b, c)

	// Room for any two of the three plans: the least recently used goes.
	cache = NewPlanCache(sizes[0] + sizes[1] + sizes[2] - 1)
	get(cache, a)
	get(cache, b)
	get(cache, a)
	want(cache, a, b)
	get(cache, c)
	want(cache, c, a)
	get(cache, b)
	want(cache, b, c)

	// The newest plan is kept even when it alone exceeds the limit.
	cache = NewPlanCache(1)
	get(cache, a)
	want(cache, a)
	get(cache, c)
	want(cache, c)
}
