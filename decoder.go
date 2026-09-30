package graptorq

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// Decoder recovers an object from encoding packets. It is not safe for
// concurrent use, but the BlockDecoders returned by Block may be driven
// from different goroutines (one goroutine per block).
type Decoder struct {
	oti    OTI
	layout Layout
	o      options
	eager  bool

	blocks  []*BlockDecoder
	pending int // blocks not complete: not decoded, or (with w) not written yet

	w       io.WriterAt // NewDecoderWriterAt: where decoded blocks are written
	written []bool      // per block, with w: written out and released
}

// NewDecoder returns a decoder for the object described by oti, which keeps
// the decoded object in memory. Unless SetDeferredDecode(true) is called,
// each source block is decoded as soon as enough symbols for it arrive.
func NewDecoder(oti OTI, opts ...Option) (*Decoder, error) {
	l, err := oti.Layout()
	if err != nil {
		return nil, err
	}
	d := &Decoder{oti: oti, layout: l, o: buildOptions(opts), eager: true}
	d.blocks = make([]*BlockDecoder, l.SourceBlocks())
	d.pending = len(d.blocks)
	return d, nil
}

// NewDecoderWriterAt returns a decoder that writes each source block to w,
// at its offset in the object, as soon as it is decoded, and then releases
// the block's memory, so that objects larger than memory can be decoded:
// only the blocks still being received are held. Decoded reports when the
// whole object has been written; AppendObject and WriteTo return
// ErrStreamed. If writing a block fails, the error is returned and the block
// is kept; Decode retries writing it.
func NewDecoderWriterAt(w io.WriterAt, oti OTI, opts ...Option) (*Decoder, error) {
	d, err := NewDecoder(oti, opts...)
	if err != nil {
		return nil, err
	}
	d.w = w
	d.written = make([]bool, len(d.blocks))
	return d, nil
}

// SetDeferredDecode turns off decoding inside AddSymbol and AddPacket;
// decoding then only happens in Decode, where blocks are decoded in parallel.
func (d *Decoder) SetDeferredDecode(deferred bool) { d.eager = !deferred }

// OTI returns the object transmission information.
func (d *Decoder) OTI() OTI { return d.oti }

// Block returns the decoder of source block sbn, creating it if needed. For
// objects with more than one sub-block its symbols are the interleaved
// symbols of RFC 6330.
func (d *Decoder) Block(sbn uint8) (*BlockDecoder, error) {
	if int(sbn) >= len(d.blocks) {
		return nil, ErrSBNRange
	}
	if b := d.blocks[sbn]; b != nil {
		return b, nil
	}
	info := d.layout.Block(sbn)
	if _, err := checkBlock(info.K, d.layout.SymbolSize); err != nil {
		return nil, err
	}
	length := int(info.Length) // fits: Length <= K*T, checked above
	if d.layout.interleaved() {
		length = info.K * d.layout.SymbolSize
	}
	o := d.o
	o.concurrency = max(1, o.concurrency/len(d.blocks))
	b, err := newBlockDecoder(length, d.layout.SymbolSize, sbn, o)
	if err != nil {
		return nil, err
	}
	d.blocks[sbn] = b
	return b, nil
}

// AddPacket adds an encoding packet: a FEC Payload ID followed by one or
// more consecutive encoding symbols (RFC 6330 Section 4.4.2). It reports
// whether the whole object has been decoded. As the RFC allows, the last
// symbol of a source packet may leave out padding octets at its end; they are
// restored as zeros.
func (d *Decoder) AddPacket(pkt []byte) (done bool, err error) {
	id, err := ParsePayloadID(pkt)
	if err != nil {
		return d.pending == 0, err
	}
	payload := pkt[PayloadIDSize:]
	T := d.layout.SymbolSize
	n := (len(payload) + T - 1) / T
	if n == 0 {
		return d.pending == 0, ErrSymbolSize
	}
	if short := n*T - len(payload); short > 0 {
		if int(id.SBN) >= d.layout.SourceBlocks() {
			return d.pending == 0, ErrSBNRange
		}
		b := d.layout.Block(id.SBN)
		if last := int(id.ESI) + n - 1; last >= b.K || short > d.layout.trailingPadding(b, last) {
			return d.pending == 0, ErrSymbolSize
		}
	}
	for i := range n {
		sym := payload[i*T : min((i+1)*T, len(payload))]
		if len(sym) < T {
			full := make([]byte, T)
			copy(full, sym)
			sym = full
		}
		if done, err = d.AddSymbol(PayloadID{SBN: id.SBN, ESI: id.ESI + uint32(i)}, sym); err != nil {
			return done, err
		}
	}
	return done, nil
}

// AddSymbol adds one encoding symbol. It reports whether the whole object has
// been decoded. A block that fails to decode for lack of symbols is not an
// error: decoding is retried as more symbols arrive.
func (d *Decoder) AddSymbol(id PayloadID, sym []byte) (done bool, err error) {
	b, err := d.Block(id.SBN)
	if err != nil {
		return d.pending == 0, err
	}
	if b.decoded {
		return d.pending == 0, nil
	}
	added, err := b.AddSymbol(id.ESI, sym)
	if err != nil || !added {
		return d.pending == 0, err
	}
	if d.eager && b.Received() >= b.K() {
		if err := b.Decode(); err == nil {
			if err := d.writeOut(id.SBN); err != nil {
				return false, err
			}
			d.pending--
		} else if !errors.Is(err, ErrInsufficientSymbols) {
			return false, err
		}
	}
	return d.pending == 0, nil
}

// writeOut writes decoded block sbn to the WriterAt, if there is one, and
// releases its memory. It is safe to call concurrently for different blocks:
// io.WriterAt allows parallel writes to non-overlapping ranges.
func (d *Decoder) writeOut(sbn uint8) error {
	if d.w == nil || d.written[sbn] {
		return nil
	}
	b := d.blocks[sbn]
	info := d.layout.Block(sbn)
	var buf []byte
	if d.layout.interleaved() {
		buf = make([]byte, info.Length)
		d.layout.scatter(buf, b.source, info.K)
	} else {
		buf, _ = b.AppendSource(make([]byte, 0, info.Length))
	}
	if _, err := d.w.WriteAt(buf, int64(info.Offset)); err != nil {
		return fmt.Errorf("graptorq: writing source block %d: %w", sbn, err)
	}
	b.release()
	d.written[sbn] = true
	return nil
}

// Decoded reports whether every source block has been decoded (and, for a
// decoder from NewDecoderWriterAt, written).
func (d *Decoder) Decoded() bool { return d.pending == 0 }

// Decode decodes every block that has at least K symbols, in parallel, and
// writes decoded blocks to the WriterAt of NewDecoderWriterAt. It returns nil
// when the whole object is complete; otherwise it returns the first error
// writing a block, or else an error wrapping ErrInsufficientSymbols (a
// *DecodeError for the first block that could not be decoded).
func (d *Decoder) Decode(ctx context.Context) error {
	errs := make([]error, len(d.blocks))
	err := parallel(ctx, len(d.blocks), d.o.concurrency, func(i int) error {
		b := d.blocks[i]
		switch {
		case b == nil:
			errs[i] = &DecodeError{SBN: uint8(i), Needed: d.layout.Block(uint8(i)).K}
		case !b.decoded:
			errs[i] = b.Decode()
		}
		if errs[i] == nil {
			errs[i] = d.writeOut(uint8(i))
		}
		return nil
	})
	if err != nil {
		return err
	}
	d.pending = 0
	var first, writeErr error
	for i, e := range errs {
		if e != nil || !d.complete(i) {
			d.pending++
			if first == nil {
				first = e
			}
			if writeErr == nil && e != nil && !errors.Is(e, ErrInsufficientSymbols) {
				writeErr = e
			}
		}
	}
	if writeErr != nil {
		return writeErr
	}
	return first
}

// complete reports whether block i is done: decoded, and written out if
// there is a WriterAt.
func (d *Decoder) complete(i int) bool {
	b := d.blocks[i]
	return b != nil && b.decoded && (d.w == nil || d.written[i])
}

// AppendObject appends the decoded object to dst.
func (d *Decoder) AppendObject(dst []byte) ([]byte, error) {
	switch {
	case d.w != nil:
		return dst, ErrStreamed
	case d.pending != 0:
		return dst, ErrNotDecoded
	}
	for sbn := range d.blocks {
		info := d.layout.Block(uint8(sbn))
		b := d.blocks[sbn]
		if !d.layout.interleaved() {
			dst, _ = b.AppendSource(dst)
			continue
		}
		n := len(dst)
		dst = grow(dst, int(info.Length))
		d.layout.scatter(dst[n:], b.source, info.K)
	}
	return dst, nil
}

// WriteTo writes the decoded object to w.
func (d *Decoder) WriteTo(w io.Writer) (int64, error) {
	switch {
	case d.w != nil:
		return 0, ErrStreamed
	case d.pending != 0:
		return 0, ErrNotDecoded
	}
	var total int64
	var buf []byte
	for sbn := range d.blocks {
		info := d.layout.Block(uint8(sbn))
		b := d.blocks[sbn]
		if d.layout.interleaved() {
			buf = grow(buf[:0], int(info.Length))
			d.layout.scatter(buf, b.source, info.K)
		} else {
			buf, _ = b.AppendSource(buf[:0])
		}
		n, err := w.Write(buf)
		total += int64(n)
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// Reset discards all received symbols so the decoder can be reused for
// another object with the same OTI.
func (d *Decoder) Reset() {
	for _, b := range d.blocks {
		if b != nil {
			b.Reset()
		}
	}
	clear(d.written)
	d.pending = len(d.blocks)
}
