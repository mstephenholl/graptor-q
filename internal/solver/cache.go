package solver

import "sync"

// syncCache maps K' to a value of type V that is computed once and then read
// many times, possibly concurrently. It wraps a sync.Map, whose reads do not
// block each other. Only values of type V can be stored, so the type
// assertions on the way out cannot fail.
type syncCache[V any] struct{ m sync.Map }

// load returns the value stored for kPrime, if any.
func (c *syncCache[V]) load(kPrime int) (V, bool) {
	v, _ := c.m.Load(kPrime) // nil when absent, which fails the assertion
	val, ok := v.(V)
	return val, ok
}

// loadOrStore returns the value stored for kPrime, storing v first if there
// is none. When goroutines race to store, all of them get the first value.
func (c *syncCache[V]) loadOrStore(kPrime int, v V) V {
	actual, _ := c.m.LoadOrStore(kPrime, v)
	if val, ok := actual.(V); ok {
		return val
	}
	return v
}
