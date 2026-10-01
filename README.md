# graptor-q

A pure Go implementation of RaptorQ, the fountain code of
[RFC 6330](https://www.rfc-editor.org/rfc/rfc6330). It covers the core codec
and the object-delivery layer, has no dependencies, and has SIMD kernels for
amd64 (AVX2, GFNI) and arm64 (NEON).

```go
import "github.com/mstephenholl/graptor-q" // package graptorq

oti, _ := graptorq.DeriveOTI(uint64(len(object)), graptorq.Config{PayloadSize: 1024})
enc, _ := graptorq.NewEncoder(object, oti)
for id, symbol := range enc.Packets(repairPerBlock) { /* send id + symbol */ }

dec, _ := graptorq.NewDecoder(oti)
done, err := dec.AddPacket(packet) // repeat until done
object, err = dec.AppendObject(nil)
```

## Features

- **Full RFC 6330.**
  - Object Transmission Information (12-byte OTI, including erratum 5548).
  - FEC Payload IDs.
  - Source block and sub-block partitioning with symbol alignment and sub-symbol interleaving (§4.4).
  - The §4.3 parameter derivation.
  - Any K up to 56,403 per block, any ESI up to 2²⁴−1.
- **Streaming for objects larger than memory.** `NewEncoderReaderAt` reads source blocks from an `io.ReaderAt` on demand, and `WithBlockCache` bounds how many stay ready. `NewDecoderWriterAt` writes each block to an `io.WriterAt` as soon as it decodes, then frees its memory. `*os.File` works for both. Streaming a 64 MB file this way grows the heap by about 1.5 MB.
- **Block-level API.** `BlockEncoder` and `BlockDecoder` work on a single source block with any symbol size.
- **Packets and limits.**
  - `AppendSymbols` builds packets that carry several consecutive symbols (RFC 6330 §4.4.2), and `AppendRepair` generates the next n repair symbols of a block.
  - Source symbols may leave out the padding at their end, as §4.4.2 allows. `WithTrimmedPadding` makes encoders do so. Decoders accept such symbols in `AddPacket` and `AddSymbol`, and reject a symbol that leaves out anything else. With sub-blocks, several symbols of the last source block can end with padding.
  - For untrusted input, `WithMaxOverhead` caps the symbols stored per block and `WithMaxMemory` caps each block's memory.
- **Fast solver.**
  - The solver compiles inactivation decoding into a straight-line *plan* of symbol operations.
  - Encoding plans depend only on K', so the library caches them for reuse across blocks.
  - Decoders compute only the intermediate symbols needed for the missing source symbols.
  - When only a few source symbols are missing, decoders skip the symbolic solve. They reuse the cached encoding plan and solve a small dense system of at most m+20 equations for the m missing symbols. This path is 1.3–2.8× faster than the full solver, and a measured cost model decides when to take it.
  - Decoders reuse all of their working memory. After `Reset`, decoding a block allocates nothing.
  - Large symbols are processed in parallel byte stripes.
- **Kernels.**

  | Tier | Selection | MulAdd throughput (16 KiB, one core) |
  |---|---|---|
  | GFNI (`VGF2P8AFFINEQB`) | amd64 with AVX2 and GFNI | 74 GB/s |
  | AVX2 (`VPSHUFB` nibble tables) | amd64 with AVX2 | 45 GB/s |
  | SSSE3 (`PSHUFB` nibble tables) | amd64 with SSSE3 but no AVX2 | 27 GB/s |
  | NEON (`TBL` nibble tables) | arm64 | not measured |
  | Generic Go | any CPU, or forced with `-tags purego` | 3 GB/s |

  The library picks the best supported tier at startup. `GRAPTORQ_GF256=<tier>` forces a tier for testing, and `make test-tiers` runs the suite on each one.

  The GFNI tier cannot use `GF2P8MULB`, because that instruction hard-codes the AES polynomial 0x11B and RFC 6330 uses 0x11D.
- **No dependencies.** The library detects CPU features with CPUID directly, because `x/sys/cpu` cannot report GFNI without AVX-512.

## More documentation

- [docs/validation.md](docs/validation.md): how the implementation is checked against RFC 6330 and other libraries.
- [docs/performance.md](docs/performance.md): benchmarks against other RaptorQ libraries.
- [docs/development.md](docs/development.md): repository layout and make targets.

## Roadmap

See [ROADMAP.md](ROADMAP.md) for planned work, including further
performance investigation.

## Notes

- RFC 6330 has intellectual property disclosures on file with the IETF,
  including from Qualcomm. Review them before deploying.
- Credits:
  - The first known-answer vectors come from github.com/takeyourhatoff/raptorq (MIT), which generated them with cberner/raptorq.
  - The golden vectors come from cberner/raptorq (Apache-2.0).
