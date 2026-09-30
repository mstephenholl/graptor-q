package graptorq

// Layout is the partitioning of an object into source blocks and sub-blocks
// (RFC 6330 Section 4.4.1.2) described by an OTI.
type Layout struct {
	TransferLength uint64
	SymbolSize     int
	Kt             int // total number of source symbols
	KL, KS         int // symbols in the first ZL blocks and in the remaining ZS blocks
	ZL, ZS         int
	TL, TS         int // sub-symbol sizes in bytes of the first NL and remaining NS sub-blocks
	NL, NS         int
}

// BlockInfo describes one source block.
type BlockInfo struct {
	SBN    uint8
	K      int    // number of source symbols
	Offset uint64 // position of the block in the object
	Length int64  // bytes of object data in the block (K*T except possibly the last block)
}

// Layout validates o and returns the partitioning it describes.
func (o OTI) Layout() (Layout, error) {
	if err := o.Validate(); err != nil {
		return Layout{}, err
	}
	T := int(o.SymbolSize)
	l := Layout{TransferLength: o.TransferLength, SymbolSize: T}
	l.Kt = int(ceilDiv(o.TransferLength, uint64(T)))
	l.KL, l.KS, l.ZL, l.ZS = Partition(l.Kt, int(o.SourceBlocks))
	al := int(o.Alignment)
	l.TL, l.TS, l.NL, l.NS = Partition(T/al, int(o.SubBlocks))
	l.TL *= al
	l.TS *= al
	return l, nil
}

// SourceBlocks returns the number of source blocks Z.
func (l *Layout) SourceBlocks() int { return l.ZL + l.ZS }

// Block returns the description of source block sbn, which must be less than
// SourceBlocks.
func (l *Layout) Block(sbn uint8) BlockInfo {
	b := BlockInfo{SBN: sbn}
	i := int(sbn)
	var symbols int
	if i < l.ZL {
		b.K = l.KL
		symbols = i * l.KL
	} else {
		b.K = l.KS
		symbols = l.ZL*l.KL + (i-l.ZL)*l.KS
	}
	b.Offset = uint64(symbols) * uint64(l.SymbolSize)
	b.Length = int64(min(uint64(b.K)*uint64(l.SymbolSize), l.TransferLength-b.Offset))
	return b
}

// interleaved reports whether symbols are not contiguous in the object.
func (l *Layout) interleaved() bool { return l.NL+l.NS > 1 }

// subBlocks calls f for each sub-block with its sub-symbol size and its
// offset within a symbol.
func (l *Layout) subBlocks(f func(size, off int)) {
	off := 0
	for n := range l.NL + l.NS {
		size := l.TL
		if n >= l.NL {
			size = l.TS
		}
		f(size, off)
		off += size
	}
}

// trailingPadding returns how many octets at the end of source symbol m of b
// are padding rather than object data. The object is padded to Kt*T octets at
// the end of its last source block (RFC 6330 Section 4.4.1.2); with
// sub-blocks, that padding can reach the end of several symbols.
func (l *Layout) trailingPadding(b BlockInfo, m int) int {
	pad := 0
	for n := l.NL + l.NS - 1; n >= 0; n-- {
		size, off := l.TL, n*l.TL
		if n >= l.NL {
			size, off = l.TS, l.NL*l.TL+(n-l.NL)*l.TS
		}
		// End of sub-symbol m of sub-block n within the block.
		end := int64(b.K)*int64(off) + int64(m+1)*int64(size)
		p := int(min(max(end-b.Length, 0), int64(size)))
		pad += p
		if p < size {
			break
		}
	}
	return pad
}

// gather converts a source block as laid out in the object (block, which may
// be shorter than K*T: the rest is zero padding) into K contiguous symbols:
// symbol m is the concatenation of sub-symbol m of every sub-block.
func (l *Layout) gather(dst, block []byte, K int) {
	T := l.SymbolSize
	clear(dst[:K*T])
	l.subBlocks(func(size, off int) {
		base := K * off // sub-block start within the block
		for m := range K {
			s := base + m*size
			if s >= len(block) {
				return
			}
			copy(dst[m*T+off:m*T+off+size], block[s:min(s+size, len(block))])
		}
	})
}

// scatter is the inverse of gather: it writes the object bytes of a block
// (len(block) bytes) from its K symbols, symbol m being symbol(m).
func (l *Layout) scatter(block []byte, symbol func(m int) []byte, K int) {
	l.subBlocks(func(size, off int) {
		base := K * off
		for m := range K {
			s := base + m*size
			if s >= len(block) {
				return
			}
			copy(block[s:min(s+size, len(block))], symbol(m)[off:off+size])
		}
	})
}
