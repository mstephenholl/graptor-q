//go:build race

package graptorq

// raceEnabled reports whether the race detector is on. It makes sync.Pool
// drop items at random, so allocation counts are not meaningful.
const raceEnabled = true
