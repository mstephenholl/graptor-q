package graptorq

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidOTI is returned for malformed or inconsistent Object
	// Transmission Information.
	ErrInvalidOTI = errors.New("graptorq: invalid object transmission information")
	// ErrInvalidParameters is returned for unsupported coding parameters.
	ErrInvalidParameters = errors.New("graptorq: invalid parameters")
	// ErrSymbolSize is returned when a symbol does not have the symbol size
	// of its source block.
	ErrSymbolSize = errors.New("graptorq: wrong symbol size")
	// ErrESIRange is returned for an encoding symbol ID above MaxESI.
	ErrESIRange = errors.New("graptorq: encoding symbol ID out of range")
	// ErrSBNRange is returned for a source block number that does not exist.
	ErrSBNRange = errors.New("graptorq: source block number out of range")
	// ErrInsufficientSymbols is returned when decoding needs more symbols.
	// It is not permanent: add more symbols and decode again.
	ErrInsufficientSymbols = errors.New("graptorq: not enough symbols to decode")
	// ErrMemoryLimit is returned when a block would need more working
	// memory than allowed by WithMaxMemory.
	ErrMemoryLimit = errors.New("graptorq: memory limit exceeded")
	// ErrStreamed is returned when reading decoded data that has already
	// been written to the io.WriterAt of NewDecoderWriterAt and released.
	ErrStreamed = errors.New("graptorq: decoded data was written out and released")
	// ErrNotDecoded is returned when reading data that is not decoded yet.
	ErrNotDecoded = errors.New("graptorq: not decoded")
)

// ParamError describes an invalid parameter value. It wraps
// ErrInvalidParameters.
type ParamError struct {
	Param  string
	Value  uint64
	Reason string
}

func (e *ParamError) Error() string {
	return fmt.Sprintf("graptorq: invalid %s %d: %s", e.Param, e.Value, e.Reason)
}

func (e *ParamError) Unwrap() error { return ErrInvalidParameters }

// DecodeError reports a source block that could not be decoded yet. It wraps
// ErrInsufficientSymbols.
type DecodeError struct {
	SBN      uint8
	Received int // distinct encoding symbols received
	Needed   int // K, the minimum number of symbols (decoding may need a few more)
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("graptorq: source block %d: cannot decode from %d symbols (K=%d)", e.SBN, e.Received, e.Needed)
}

func (e *DecodeError) Unwrap() error { return ErrInsufficientSymbols }
