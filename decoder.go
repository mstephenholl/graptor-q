package graptorq

import (
	"context"
	"errors"
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
	pending int // blocks not decoded yet
}

// NewDecoder returns a decoder for the object described by oti. Unless
// WithDeferredDecode is given, each source block is decoded as soon as
// enough symbols for it have arrived.
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
// whether the whole object has been decoded.
func (d *Decoder) AddPacket(pkt []byte) (done bool, err error) {
	id, err := ParsePayloadID(pkt)
	if err != nil {
		return d.pending == 0, err
	}
	payload := pkt[PayloadIDSize:]
	T := d.layout.SymbolSize
	if len(payload) == 0 || len(payload)%T != 0 {
		return d.pending == 0, ErrSymbolSize
	}
	for i := 0; i < len(payload); i += T {
		if done, err = d.AddSymbol(PayloadID{SBN: id.SBN, ESI: id.ESI + uint32(i/T)}, payload[i:i+T]); err != nil {
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
			d.pending--
		} else if !errors.Is(err, ErrInsufficientSymbols) {
			return false, err
		}
	}
	return d.pending == 0, nil
}

// Decoded reports whether every source block has been decoded.
func (d *Decoder) Decoded() bool { return d.pending == 0 }

// Decode decodes every block that has at least K symbols, in parallel. It
// returns nil when the whole object is decoded, and otherwise an error
// wrapping ErrInsufficientSymbols (a *DecodeError for the first block that
// could not be decoded).
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
		return nil
	})
	if err != nil {
		return err
	}
	d.pending = 0
	var first error
	for i, e := range errs {
		if e != nil || (d.blocks[i] != nil && !d.blocks[i].decoded) {
			d.pending++
			if first == nil {
				first = e
			}
		}
	}
	return first
}

// AppendObject appends the decoded object to dst.
func (d *Decoder) AppendObject(dst []byte) ([]byte, error) {
	if d.pending != 0 {
		return dst, ErrNotDecoded
	}
	for sbn := range d.blocks {
		info := d.layout.Block(uint8(sbn))
		b := d.blocks[sbn]
		if !d.layout.interleaved() {
			dst = append(dst, b.out[:info.Length]...)
			continue
		}
		n := len(dst)
		dst = grow(dst, int(info.Length))
		d.layout.scatter(dst[n:], b.out, info.K)
	}
	return dst, nil
}

// WriteTo writes the decoded object to w.
func (d *Decoder) WriteTo(w io.Writer) (int64, error) {
	if d.pending != 0 {
		return 0, ErrNotDecoded
	}
	var total int64
	var buf []byte
	for sbn := range d.blocks {
		info := d.layout.Block(uint8(sbn))
		b := d.blocks[sbn]
		out := b.out[:info.Length]
		if d.layout.interleaved() {
			buf = grow(buf[:0], int(info.Length))
			d.layout.scatter(buf, b.out, info.K)
			out = buf
		}
		n, err := w.Write(out)
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
	d.pending = len(d.blocks)
}
