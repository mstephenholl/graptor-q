package graptorq

import (
	"encoding/binary"
	"fmt"
)

// OTI is the Object Transmission Information of RFC 6330 Section 3.3: the
// parameters a receiver needs to decode an object.
type OTI struct {
	TransferLength uint64 // F: object size in bytes, 1..MaxTransferLength
	SymbolSize     uint16 // T: symbol size in bytes, a multiple of Alignment
	SourceBlocks   uint8  // Z: number of source blocks
	SubBlocks      uint16 // N: number of sub-blocks per source block
	Alignment      uint8  // Al: symbol alignment in bytes
}

// Validate checks that o describes a valid partitioning of the object.
//
// Beyond the field ranges of RFC 6330, it rejects parameter combinations
// that the RFC leaves degenerate: more source blocks than symbols (empty
// blocks) and more sub-blocks than T/Al (empty sub-symbols).
func (o OTI) Validate() error {
	switch {
	case o.TransferLength == 0 || o.TransferLength > MaxTransferLength:
		return fmt.Errorf("%w: transfer length %d not in [1, %d]", ErrInvalidOTI, o.TransferLength, uint64(MaxTransferLength))
	case o.Alignment == 0:
		return fmt.Errorf("%w: zero alignment", ErrInvalidOTI)
	case o.SymbolSize == 0 || o.SymbolSize%uint16(o.Alignment) != 0:
		return fmt.Errorf("%w: symbol size %d is not a positive multiple of alignment %d", ErrInvalidOTI, o.SymbolSize, o.Alignment)
	case o.SourceBlocks == 0:
		return fmt.Errorf("%w: zero source blocks", ErrInvalidOTI)
	case o.SubBlocks == 0 || int(o.SubBlocks) > int(o.SymbolSize)/int(o.Alignment):
		return fmt.Errorf("%w: %d sub-blocks not in [1, T/Al = %d]", ErrInvalidOTI, o.SubBlocks, int(o.SymbolSize)/int(o.Alignment))
	}
	kt := ceilDiv(o.TransferLength, uint64(o.SymbolSize))
	if kt < uint64(o.SourceBlocks) {
		return fmt.Errorf("%w: %d source blocks for %d symbols", ErrInvalidOTI, o.SourceBlocks, kt)
	}
	if ceilDiv(kt, uint64(o.SourceBlocks)) > MaxSourceSymbols {
		return fmt.Errorf("%w: more than %d symbols per source block", ErrInvalidOTI, MaxSourceSymbols)
	}
	return nil
}

// AppendBinary appends the 12-byte encoding of o (the Common FEC OTI of
// Section 3.3.2 followed by the Scheme-Specific FEC OTI of Section 3.3.3).
func (o OTI) AppendBinary(b []byte) ([]byte, error) {
	if o.TransferLength >= 1<<40 {
		return b, fmt.Errorf("%w: transfer length exceeds 40 bits", ErrInvalidOTI)
	}
	c, s := o.CommonFEC(), o.SchemeSpecific()
	return append(append(b, c[:]...), s[:]...), nil
}

// MarshalBinary returns the 12-byte encoding of o.
func (o OTI) MarshalBinary() ([]byte, error) { return o.AppendBinary(make([]byte, 0, OTISize)) }

// UnmarshalBinary decodes a 12-byte OTI and validates it.
func (o *OTI) UnmarshalBinary(b []byte) error {
	v, err := ParseOTI(b)
	if err != nil {
		return err
	}
	*o = v
	return nil
}

// CommonFEC returns the 8-byte Common FEC OTI element (Section 3.3.2):
// a 40-bit transfer length, a reserved octet (zero) and the 16-bit symbol size.
func (o OTI) CommonFEC() [8]byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], o.TransferLength<<24)
	binary.BigEndian.PutUint16(b[6:], o.SymbolSize)
	return b
}

// SchemeSpecific returns the 4-byte Scheme-Specific FEC OTI element
// (Section 3.3.3): Z, N and Al.
func (o OTI) SchemeSpecific() [4]byte {
	return [4]byte{o.SourceBlocks, byte(o.SubBlocks >> 8), byte(o.SubBlocks), o.Alignment}
}

// ParseOTI decodes and validates a 12-byte OTI. The reserved octet is ignored.
func ParseOTI(b []byte) (OTI, error) {
	if len(b) != OTISize {
		return OTI{}, fmt.Errorf("%w: %d bytes, want %d", ErrInvalidOTI, len(b), OTISize)
	}
	return OTIFromParts([8]byte(b[:8]), [4]byte(b[8:]))
}

// OTIFromParts decodes and validates an OTI carried as its separate Common
// and Scheme-Specific elements (as in FLUTE/ALC).
func OTIFromParts(common [8]byte, schemeSpecific [4]byte) (OTI, error) {
	o := OTI{
		TransferLength: binary.BigEndian.Uint64(common[:]) >> 24,
		SymbolSize:     binary.BigEndian.Uint16(common[6:]),
		SourceBlocks:   schemeSpecific[0],
		SubBlocks:      binary.BigEndian.Uint16(schemeSpecific[1:]),
		Alignment:      schemeSpecific[3],
	}
	return o, o.Validate()
}

// PayloadID is the FEC Payload ID of RFC 6330 Section 3.2: the source block
// number and encoding symbol ID of the symbol(s) in a packet.
type PayloadID struct {
	SBN uint8
	ESI uint32 // 0..MaxESI
}

// AppendBinary appends the 4-byte encoding of p.
func (p PayloadID) AppendBinary(b []byte) ([]byte, error) {
	if p.ESI > MaxESI {
		return b, ErrESIRange
	}
	return append(b, p.SBN, byte(p.ESI>>16), byte(p.ESI>>8), byte(p.ESI)), nil
}

// ParsePayloadID decodes the 4-byte FEC Payload ID at the start of b.
func ParsePayloadID(b []byte) (PayloadID, error) {
	if len(b) < PayloadIDSize {
		return PayloadID{}, fmt.Errorf("graptorq: FEC payload ID needs %d bytes, have %d", PayloadIDSize, len(b))
	}
	return PayloadID{SBN: b[0], ESI: uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])}, nil
}

func ceilDiv(a, b uint64) uint64 { return (a + b - 1) / b }
