package graptorq

import (
	"container/list"
	"sync"

	"github.com/mholland/graptorq/internal/rfc"
	"github.com/mholland/graptorq/internal/solver"
)

// PlanCache caches the encoding plan of each extended source block size K'.
//
// Computing the intermediate symbols of a source block requires solving the
// same K'-dependent constraint matrix every time; the solution procedure (the
// plan) does not depend on the data or the symbol size, so encoders share it.
// A PlanCache is safe for concurrent use; concurrent requests for the same K'
// compute its plan only once.
type PlanCache struct {
	mu       sync.Mutex
	maxBytes int64
	bytes    int64
	entries  map[int]*planEntry
	lru      list.List // of *planEntry, most recently used first
}

type planEntry struct {
	kPrime int
	once   sync.Once
	plan   *solver.Plan
	err    error
	size   int64
	elem   *list.Element // nil until the plan is computed
}

// NewPlanCache returns a cache that keeps plans up to a total of maxBytes
// (least recently used plans are dropped first). Plans take about 160 bytes
// per source symbol: 1.5 MB for K' = 10017 and 9 MB for K' = 56403. The
// package-wide default cache holds 64 MiB.
func NewPlanCache(maxBytes int64) *PlanCache {
	return &PlanCache{maxBytes: maxBytes, entries: make(map[int]*planEntry)}
}

var defaultPlanCache = NewPlanCache(64 << 20)

func (c *PlanCache) plan(p *rfc.Params) (*solver.Plan, error) {
	c.mu.Lock()
	e, ok := c.entries[p.KPrime]
	if !ok {
		e = &planEntry{kPrime: p.KPrime}
		c.entries[p.KPrime] = e
	} else if e.elem != nil {
		c.lru.MoveToFront(e.elem)
	}
	c.mu.Unlock()

	e.once.Do(func() {
		e.plan, e.err = solver.NewPlan(p, seqISIs(p.KPrime))
		c.mu.Lock()
		defer c.mu.Unlock()
		if e.err != nil || c.entries[p.KPrime] != e {
			delete(c.entries, p.KPrime)
			return
		}
		e.size = int64(e.plan.Size())
		e.elem = c.lru.PushFront(e)
		c.bytes += e.size
		for c.bytes > c.maxBytes && c.lru.Len() > 1 {
			old := c.lru.Remove(c.lru.Back()).(*planEntry)
			delete(c.entries, old.kPrime)
			c.bytes -= old.size
		}
	})
	return e.plan, e.err
}

func seqISIs(n int) []uint32 {
	isis := make([]uint32, n)
	for i := range isis {
		isis[i] = uint32(i)
	}
	return isis
}

func (o *options) encodingPlan(p *rfc.Params) (*solver.Plan, error) {
	if o.planCache == nil {
		return solver.NewPlan(p, seqISIs(p.KPrime))
	}
	return o.planCache.plan(p)
}
