package graptorq

import "runtime"

// Option configures encoders and decoders.
type Option func(*options)

type options struct {
	concurrency int
	planCache   *PlanCache
	noPlanCache bool
	maxMemory   int64
	blockCache  int // 0: default, > 0: limit, < 0: unlimited
	maxOverhead int // symbols beyond K stored per source block, < 0: unlimited
	trimPadding bool
}

func buildOptions(opts []Option) options {
	o := options{concurrency: runtime.GOMAXPROCS(0), maxOverhead: -1}
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

// WithBlockCache limits how many source blocks an Encoder keeps ready (read
// and prepared) at once; the least recently used block is dropped when the
// limit is exceeded, and read and prepared again if it is needed later.
// n <= 0 means no limit. The default is 1 for NewEncoderReaderAt, which
// suits sending an object block after block, and no limit for NewEncoder.
func WithBlockCache(n int) Option {
	return func(o *options) {
		o.blockCache = n
		if n <= 0 {
			o.blockCache = -1
		}
	}
}

// WithMaxOverhead makes decoders store at most K+n distinct symbols per
// source block (of K source symbols); further symbols are ignored, like
// duplicates. This bounds the memory of each block, notably with deferred
// decoding, where symbols are only stored until Decode.
//
// If the first K+n symbols of a block happen not to determine it, the block
// cannot be decoded until Reset. With symbols chosen at random that happens
// with probability about 1e-2 for n = 0, 1e-4 for n = 1 and below 1e-6 for
// n >= 2 (RFC 6330 Section 5.8), so n >= 2 is recommended. A negative n means
// no limit, the default.
func WithMaxOverhead(n int) Option {
	return func(o *options) { o.maxOverhead = n }
}

// WithMaxMemory limits the working memory of a single source block (roughly
// (L+1)*T bytes for encoding, plus the received symbols for decoding).
// Blocks that would need more fail with ErrMemoryLimit. Zero means no limit.
// Decoders of untrusted OTIs should set a limit.
func WithMaxMemory(bytes int64) Option {
	return func(o *options) { o.maxMemory = bytes }
}

// WithTrimmedPadding makes an Encoder leave out the padding octets at the end
// of source symbols, as RFC 6330 Section 4.4.2 allows: AppendSymbol,
// AppendPacket and Packets return such symbols shorter than the symbol size,
// and AppendSymbols shortens the last symbol of the group. Padding only
// exists when the transfer length is not a multiple of the symbol size; it
// ends the last symbol of the last source block, and with sub-blocks the last
// few. Receivers must accept shortened symbols; graptorq's Decoder does.
// BlockEncoders ignore this option.
func WithTrimmedPadding() Option {
	return func(o *options) { o.trimPadding = true }
}
