package graptorq

import (
	"context"
	"fmt"
	"iter"
	"sync"
)

// Encoder generates the encoding symbols of an object according to its OTI.
// It is safe for concurrent use.
type Encoder struct {
	oti    OTI
	layout Layout
	data   []byte
	o      options

	mu     sync.Mutex
	blocks []*BlockEncoder
}

// NewEncoder returns an encoder for data, which must be exactly
// oti.TransferLength bytes. data is retained and must not be modified while
// the encoder is in use.
func NewEncoder(data []byte, oti OTI, opts ...Option) (*Encoder, error) {
	l, err := oti.Layout()
	if err != nil {
		return nil, err
	}
	if uint64(len(data)) != oti.TransferLength {
		return nil, fmt.Errorf("%w: data is %d bytes, OTI transfer length is %d", ErrInvalidOTI, len(data), oti.TransferLength)
	}
	e := &Encoder{oti: oti, layout: l, data: data, o: buildOptions(opts)}
	e.blocks = make([]*BlockEncoder, l.SourceBlocks())
	return e, nil
}

// OTI returns the object transmission information.
func (e *Encoder) OTI() OTI { return e.oti }

// Layout returns the partitioning of the object.
func (e *Encoder) Layout() Layout { return e.layout }

// Block returns the encoder of source block sbn. For objects with more than
// one sub-block its symbols are the interleaved symbols of RFC 6330.
func (e *Encoder) Block(sbn uint8) (*BlockEncoder, error) {
	if int(sbn) >= len(e.blocks) {
		return nil, ErrSBNRange
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if b := e.blocks[sbn]; b != nil {
		return b, nil
	}
	info := e.layout.Block(sbn)
	if _, err := checkBlock(info.K, e.layout.SymbolSize); err != nil {
		return nil, err
	}
	src := e.data[info.Offset : info.Offset+uint64(info.Length)]
	if e.layout.interleaved() {
		buf := make([]byte, info.K*e.layout.SymbolSize)
		e.layout.gather(buf, src, info.K)
		src = buf
	}
	// Blocks are prepared in parallel; each gets a share of the concurrency
	// budget for parallelism within the block.
	o := e.o
	o.concurrency = max(1, o.concurrency/len(e.blocks))
	b, err := NewBlockEncoder(src, e.layout.SymbolSize, withOptions(o))
	if err != nil {
		return nil, err
	}
	e.blocks[sbn] = b
	return b, nil
}

// Prepare computes the intermediate symbols of every source block, using up
// to the configured concurrency. Calling it is optional.
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
// encoded (for example because of WithMaxMemory); use AppendSymbol to see
// the error.
func (e *Encoder) Packets(repairPerBlock int) iter.Seq2[PayloadID, []byte] {
	return func(yield func(PayloadID, []byte) bool) {
		var buf []byte
		for sbn := range e.blocks {
			b, err := e.Block(uint8(sbn))
			if err != nil {
				return
			}
			for esi := range uint32(b.K() + repairPerBlock) {
				id := PayloadID{SBN: uint8(sbn), ESI: esi}
				if buf, err = b.AppendSymbol(buf[:0], esi); err != nil {
					return
				}
				if !yield(id, buf) {
					return
				}
			}
		}
	}
}

// withOptions passes already-built options to a nested constructor.
func withOptions(o options) Option { return func(dst *options) { *dst = o } }
