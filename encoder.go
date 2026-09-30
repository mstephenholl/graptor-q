package graptorq

import (
	"context"
	"fmt"
	"io"
	"iter"
	"sync"
)

// Encoder generates the encoding symbols of an object according to its OTI.
// It is safe for concurrent use.
type Encoder struct {
	oti    OTI
	layout Layout
	data   []byte      // the object (NewEncoder)
	src    io.ReaderAt // or where to read it from (NewEncoderReaderAt)
	o      options
	limit  int // maximum number of resident blocks, 0 for no limit

	mu       sync.Mutex
	blocks   []*BlockEncoder // resident blocks; nil if not loaded
	lastUse  []uint64
	tick     uint64
	resident int
	err      error // what stopped the last Packets iteration
}

// NewEncoder returns an encoder for data, which must be exactly
// oti.TransferLength bytes. data is retained and must not be modified while
// the encoder is in use.
func NewEncoder(data []byte, oti OTI, opts ...Option) (*Encoder, error) {
	if uint64(len(data)) != oti.TransferLength {
		return nil, fmt.Errorf("%w: data is %d bytes, OTI transfer length is %d", ErrInvalidOTI, len(data), oti.TransferLength)
	}
	return newEncoder(data, nil, oti, 0, opts)
}

// NewEncoderReaderAt returns an encoder that reads the object, of
// oti.TransferLength bytes, from r one source block at a time, so that
// objects larger than memory can be encoded. It keeps only the most recently
// used block ready unless WithBlockCache allows more: sending the object
// block after block (as Packets does) reads each block once. Errors reading
// r are returned by the methods that need the block.
func NewEncoderReaderAt(r io.ReaderAt, oti OTI, opts ...Option) (*Encoder, error) {
	return newEncoder(nil, r, oti, 1, opts)
}

func newEncoder(data []byte, src io.ReaderAt, oti OTI, defaultLimit int, opts []Option) (*Encoder, error) {
	l, err := oti.Layout()
	if err != nil {
		return nil, err
	}
	e := &Encoder{oti: oti, layout: l, data: data, src: src, o: buildOptions(opts), limit: defaultLimit}
	switch {
	case e.o.blockCache > 0:
		e.limit = e.o.blockCache
	case e.o.blockCache < 0:
		e.limit = 0
	}
	e.blocks = make([]*BlockEncoder, l.SourceBlocks())
	e.lastUse = make([]uint64, l.SourceBlocks())
	return e, nil
}

// OTI returns the object transmission information.
func (e *Encoder) OTI() OTI { return e.oti }

// Layout returns the partitioning of the object.
func (e *Encoder) Layout() Layout { return e.layout }

// Block returns the encoder of source block sbn, reading the block first if
// it is not resident. For objects with more than one sub-block its symbols
// are the interleaved symbols of RFC 6330.
func (e *Encoder) Block(sbn uint8) (*BlockEncoder, error) {
	if int(sbn) >= len(e.blocks) {
		return nil, ErrSBNRange
	}
	e.mu.Lock()
	if b := e.blocks[sbn]; b != nil {
		e.touch(sbn)
		e.mu.Unlock()
		return b, nil
	}
	e.mu.Unlock()

	b, err := e.loadBlock(sbn) // reads the source without holding the lock
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if cur := e.blocks[sbn]; cur != nil { // loaded concurrently
		e.touch(sbn)
		return cur, nil
	}
	e.blocks[sbn] = b
	e.resident++
	e.touch(sbn)
	for e.limit > 0 && e.resident > e.limit {
		e.evictLRU(sbn)
	}
	return b, nil
}

func (e *Encoder) touch(sbn uint8) {
	e.tick++
	e.lastUse[sbn] = e.tick
}

// evictLRU drops the least recently used resident block other than keep.
// Goroutines still using it keep it alive until they are done.
func (e *Encoder) evictLRU(keep uint8) {
	victim := -1
	for i, b := range e.blocks {
		if b != nil && i != int(keep) && (victim < 0 || e.lastUse[i] < e.lastUse[victim]) {
			victim = i
		}
	}
	e.blocks[victim] = nil
	e.resident--
}

// loadBlock reads source block sbn and returns its block encoder.
func (e *Encoder) loadBlock(sbn uint8) (*BlockEncoder, error) {
	info := e.layout.Block(sbn)
	if _, err := checkBlock(info.K, e.layout.SymbolSize); err != nil {
		return nil, err
	}
	var src []byte
	if e.src == nil {
		src = e.data[info.Offset : info.Offset+uint64(info.Length)]
	} else {
		src = make([]byte, info.Length)
		// A complete read may report io.EOF at the end of the source.
		if n, err := e.src.ReadAt(src, int64(info.Offset)); n < len(src) {
			if err == nil || err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, fmt.Errorf("graptorq: reading source block %d: %w", sbn, err)
		}
	}
	if e.layout.interleaved() {
		buf := make([]byte, info.K*e.layout.SymbolSize)
		e.layout.gather(buf, src, info.K)
		src = buf
	}
	// Blocks are prepared in parallel; each gets a share of the concurrency
	// budget for parallelism within the block.
	o := e.o
	o.concurrency = max(1, o.concurrency/len(e.blocks))
	return NewBlockEncoder(src, e.layout.SymbolSize, withOptions(o))
}

// Prepare computes the intermediate symbols of every source block, using up
// to the configured concurrency. Calling it is optional, and of little use
// with a block cache smaller than the number of source blocks, since blocks
// are dropped again.
func (e *Encoder) Prepare(ctx context.Context) error {
	return parallel(ctx, len(e.blocks), e.o.concurrency, func(sbn int) error {
		b, err := e.Block(uint8(sbn))
		if err != nil {
			return err
		}
		return b.Prepare()
	})
}

// parallel runs f(0..n-1) on up to limit goroutines and returns the first
// error. After an error or context cancellation, remaining calls are skipped.
func parallel(ctx context.Context, n, limit int, f func(i int) error) error {
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		first error
		next  int
	)
	fail := func(err error) {
		mu.Lock()
		if first == nil {
			first = err
		}
		mu.Unlock()
	}
	for range min(limit, n) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				i, stop := next, first != nil
				next++
				mu.Unlock()
				if stop || i >= n {
					return
				}
				if err := ctx.Err(); err != nil {
					fail(err)
					return
				}
				if err := f(i); err != nil {
					fail(err)
				}
			}
		}()
	}
	wg.Wait()
	return first
}

// AppendSymbol appends the encoding symbol identified by id to dst.
func (e *Encoder) AppendSymbol(dst []byte, id PayloadID) ([]byte, error) {
	b, err := e.Block(id.SBN)
	if err != nil {
		return dst, err
	}
	return b.AppendSymbol(dst, id.ESI)
}

// AppendSymbols appends the n consecutive encoding symbols of source block
// id.SBN starting at ESI id.ESI; with id's Payload ID in front they form a
// packet carrying several symbols (RFC 6330 Section 4.4.2).
func (e *Encoder) AppendSymbols(dst []byte, id PayloadID, n int) ([]byte, error) {
	b, err := e.Block(id.SBN)
	if err != nil {
		return dst, err
	}
	return b.AppendSymbols(dst, id.ESI, n)
}

// AppendPacket appends an encoding packet (RFC 6330 Section 4.4.2): the FEC
// Payload ID followed by the encoding symbol.
func (e *Encoder) AppendPacket(dst []byte, id PayloadID) ([]byte, error) {
	n := len(dst)
	dst, err := id.AppendBinary(dst)
	if err != nil {
		return dst, err
	}
	dst, err = e.AppendSymbol(dst, id)
	if err != nil {
		return dst[:n], err
	}
	return dst, nil
}

// Packets yields, for every source block in order, its K source symbols
// followed by repairPerBlock repair symbols. The yielded symbol slice is
// reused between iterations. Iteration stops early if a block cannot be
// read or encoded; Err then reports why.
func (e *Encoder) Packets(repairPerBlock int) iter.Seq2[PayloadID, []byte] {
	return func(yield func(PayloadID, []byte) bool) {
		e.setErr(nil)
		var buf []byte
		for sbn := range e.blocks {
			b, err := e.Block(uint8(sbn))
			if err != nil {
				e.setErr(err)
				return
			}
			for esi := range uint32(b.K() + repairPerBlock) {
				id := PayloadID{SBN: uint8(sbn), ESI: esi}
				if buf, err = b.AppendSymbol(buf[:0], esi); err != nil {
					e.setErr(err)
					return
				}
				if !yield(id, buf) {
					return
				}
			}
		}
	}
}

// Err returns the error that stopped the most recent Packets iteration, or
// nil if it was not stopped by an error.
func (e *Encoder) Err() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.err
}

func (e *Encoder) setErr(err error) {
	e.mu.Lock()
	e.err = err
	e.mu.Unlock()
}

// withOptions passes already-built options to a nested constructor.
func withOptions(o options) Option { return func(dst *options) { *dst = o } }
