package graptorq

import "runtime"

// Option configures encoders and decoders.
type Option func(*options)

type options struct {
	concurrency int
	planCache   *PlanCache
	noPlanCache bool
	maxMemory   int64
}

func buildOptions(opts []Option) options {
	o := options{concurrency: runtime.GOMAXPROCS(0)}
	for _, f := range opts {
		f(&o)
	}
	if o.planCache == nil && !o.noPlanCache {
		o.planCache = defaultPlanCache
	}
	return o
}

// WithConcurrency sets the maximum number of goroutines used to encode or
// decode. Independent source blocks are processed in parallel, and within a
// block, symbols of at least 1 KiB are split into byte ranges processed in
// parallel. The default is GOMAXPROCS; 1 disables all parallelism.
func WithConcurrency(n int) Option {
	return func(o *options) { o.concurrency = max(1, n) }
}

// WithPlanCache makes encoders share the given plan cache instead of the
// package-wide default cache.
func WithPlanCache(c *PlanCache) Option {
	return func(o *options) { o.planCache, o.noPlanCache = c, c == nil }
}

// WithoutPlanCache disables plan caching: every encoder computes its plan.
func WithoutPlanCache() Option {
	return func(o *options) { o.planCache, o.noPlanCache = nil, true }
}

// WithMaxMemory limits the working memory of a single source block (roughly
// (L+1)*T bytes for encoding, plus the received symbols for decoding).
// Blocks that would need more fail with ErrMemoryLimit. Zero means no limit.
// Decoders of untrusted OTIs should set a limit.
func WithMaxMemory(bytes int64) Option {
	return func(o *options) { o.maxMemory = bytes }
}
