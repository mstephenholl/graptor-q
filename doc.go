// Package graptorq implements the RaptorQ forward error correction scheme of
// RFC 6330 in pure Go.
//
// RaptorQ is a systematic fountain code: a source block of K symbols can be
// expanded into up to 2^24 encoding symbols, the first K of which are the
// source symbols themselves, and the block can be recovered from almost any
// K of them (with probability above 99%, and above 99.9999% with K+2).
//
// The package offers two levels of API:
//
//   - BlockEncoder and BlockDecoder operate on a single source block of up
//     to 56403 symbols of any size, identified by encoding symbol IDs (ESIs).
//   - Encoder and Decoder implement the object delivery of RFC 6330 Section
//     4: an object of up to 942574504275 bytes is partitioned into source
//     blocks and sub-blocks as described by its Object Transmission
//     Information (OTI), and symbols are identified by a FEC Payload ID
//     (source block number and ESI). This layer interoperates with any
//     other compliant implementation.
//
// GF(256) symbol arithmetic uses SIMD assembly on amd64 (AVX2, GFNI) and
// arm64 (NEON) when available. Building with the purego tag selects the
// portable Go implementation.
package graptorq
