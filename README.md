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

## Validation

| Check | What it establishes |
|---|---|
| Tables generated from the RFC text (`testdata/rfc6330.txt`, SHA-256 pinned) and re-checked on every test run | No transcription errors in V0–V3, Deg, Table 2, OCT_EXP and OCT_LOG |
| GF(256): all 65,536 products, quotients and inverses against the RFC's tables. Every kernel tier against scalar code for all 256 constants, lengths 0–260 plus large sizes, and unaligned and aliased buffers. Fuzzing | Field arithmetic and SIMD kernels |
| Dense reference solver (`internal/refsolve`): literal RFC loops, explicit MT×GAMMA product, scalar Gauss-Jordan | Independent oracle for the optimized solver |
| All 477 K' values: full rank, A·C = D residual check, systematic property | Constraint matrix construction |
| Differential against the reference solver on random received sets, including singular ones | Identical rank decisions and solutions |
| **cberner/raptorq 2.0.1 golden vectors** (`tools/rqoracle`, committed as `testdata/vectors`) | Byte-exact encoding symbols for 49 objects (3,938 symbols): Z up to 255, N up to 32 with TL≠TS, K up to 56,403, ESI 2²⁴−1. Also decoding of cberner packets, and identical §4.3 derivations |
| Live cberner interop (`make interop`) | Both directions, plus the first two repair symbols compared at **every K'** |
| **xssnick/raptorq v1.5.2 differential** (`interop/`) | Identical symbols at 378 of 477 K'. Cross-decoding in both directions (see the finding below) |
| **takeyourhatoff/raptorq differential** (`interop/`) | Identical symbols at all 477 K', with and without its SIMD kernels. It derives from xssnick/raptorq but does not share its P1 deviation. Cross-decoding in both directions |
| **fgn/raptorgo v0.1.1 differential** (`interop/`), an independent implementation with the object layer | Identical §4.3 derivations for 52,608 inputs. Byte-identical packets, single and grouped, source and repair, with and without the padding at the end of source symbols, for 20 objects with Z up to 5, N up to 13 and Al from 1 to 8. Cross-decoding of whole objects in both directions, with and without that padding |
| §5.8 recovery properties, with ESIs uniform over 0..2²⁴−1 (`make stat-long`) | Failures at K' symbols: 491/10⁵ = 0.49% (bound 1%). At K'+1: 4/(3×10⁵) = 1.3×10⁻⁵ (bound 10⁻⁴). At K'+2: 0/10⁶ (bound 10⁻⁶) |
| Fuzzing: OTI parsing, garbage packets, block round trips | Robustness on untrusted input |
| Platforms: amd64, `purego`, arm64 (natively in CI and under qemu), s390x (big-endian, qemu), 386 | Portability |

### Finding: xssnick/raptorq deviates from RFC 6330 at 99 of the 477 K'

xssnick/raptorq v1.5.2 (`params.go`) computes P1 as the smallest prime *strictly
greater* than P:

```go
p._P1 = p._P + 1
for !isPrime(p._P1) { p._P1++ }
```

RFC 6330 §5.3.3.3 defines P1 as the smallest prime *greater than or equal to* P. The two
differ exactly when P is prime. The first such K' is 49. At those sizes
its repair symbols differ from every RFC-compliant implementation (graptor-q and
cberner agree there). Its encoder and decoder share the deviation, so it still
round-trips with itself, but it does not interoperate at those block sizes.
`interop/xssnick_test.go` documents this and fails if the behaviour changes.

## Performance

The figures are MB/s of source data on a single core (an Intel Core Ultra 7
155U P-core, GOMAXPROCS=1), the median of 5 runs with Go 1.27.1.

- **Encode.** Build the encoder for a new block, then generate one repair symbol. The solution procedure depends only on K', and three libraries reuse it across blocks: graptor-q through its plan cache (the default), takeyourhatoff through `Encoder.Reset`, and cberner through the process-wide plan cache of `SourceBlockEncoder::new`. The "from scratch" rows solve every block anew (`WithoutPlanCache()` for graptor-q, a new `Encoder` for takeyourhatoff, `SourceBlockEncodingPlan::generate` for cberner), as xssnick and raptorgo always do.
- **Decode.** Lose 10% of the source symbols, replace them with that many repair symbols plus two, decode, and deliver the block into a reused buffer. raptorgo cannot reuse a decoder, so its figures include creating one per block.
- **SIMD builds.** takeyourhatoff and raptorgo are measured with their AVX2 kernels, built with `GOEXPERIMENT=simd GOAMD64=v3`. takeyourhatoff also has AVX-512 kernels, which this CPU cannot run. raptorgo's need Go 1.26 and were measured with Go 1.26.5. Without the experiment they reach 43–153 MB/s (takeyourhatoff) and 7–63 MB/s (raptorgo). graptor-q and xssnick do not use the experiment, and their figures change by at most 5% under it.
- **raptorgo** has only an object API, so its figures use an OTI with a single source block. graptor-q's object API is within 4% of its block API on the same benchmarks.

| | K | T | graptor-q | xssnick v1.5.2 | takeyourhatoff | raptorgo v0.1.1 | cberner 2.0.1 |
|---|---:|---:|---:|---:|---:|---:|---:|
| encode, plan reused | 100 | 1280 | **2722** | – | 1483 | – | 1137 |
| encode, plan reused | 1000 | 1280 | **2586** | – | 1398 | – | 1214 |
| encode, plan reused | 10000 | 1280 | **1021** | – | 760 | – | 680 |
| encode, plan reused | 50000 | 256 | **515** | – | 452 | – | 310 |
| encode, from scratch | 100 | 1280 | **1657** | 889 | 800 | 263 | 449 |
| encode, from scratch | 1000 | 1280 | **1420** | 776 | 703 | 185 | 521 |
| encode, from scratch | 10000 | 1280 | **749** | 491 | 489 | 118 | 358 |
| encode, from scratch | 50000 | 256 | **231** | 111 | 113 | 19 | 71 |
| decode | 100 | 1280 | **2005** | 907 | 872 | 263 | 586 |
| decode | 1000 | 1280 | **1446** | 830 | 848 | 222 | 415 |
| decode | 10000 | 1280 | **694** | 472 | 495 | 113 | 339 |
| decode | 50000 | 256 | **211** | 107 | 117 | 13 | 71 |

The cberner figures are the median of 3 runs of its own benchmark (`rqoracle bench`).
`make bench-compare CPU=<a performance core>` reproduces every figure except
raptorgo's SIMD build, which does not compile with Go 1.27
(see `interop/raptorgo_simd_test.go`).

With `WithConcurrency(n)`, a large block is processed in parallel byte
stripes. At T=1280, 4 goroutines make encoding 40–75% faster.

## Repository layout

| Path | Contents |
|---|---|
| `*.go` | Public API: OTI, Payload ID, §4.3 derivation, layout, block and object encoders and decoders, plan cache |
| `internal/gf256` | GF(256) arithmetic and kernels (generic, AVX2, GFNI, NEON) |
| `internal/rfc` | Table 2, Rand, Deg, Tuple, row patterns. `gentables` generates `tables_gen.go` from the RFC text |
| `internal/solver` | Symbolic inactivation decoding, the plan, and the §5.8 statistics test |
| `internal/refsolve` | Dense reference implementation (test oracle only) |
| `interop/` | Separate module: differential tests against xssnick, takeyourhatoff and raptorgo, live cberner tests, comparison benchmarks |
| `tools/rqoracle` | Rust oracle built on cberner/raptorq 2.0.1: golden vectors, encoding and decoding for the interop tests, and benchmarks. Builds with Cargo or, without a Rust toolchain, with its Dockerfile |

The main make targets are `test`, `test-purego`, `test-race`, `test-cross`
(arm64, s390x, 386), `lint` (golangci-lint v2.14), `interop`, `oracle-vectors`,
`bench-compare`, `stat-long`, and `fuzz`. The targets that need the oracle build it with the local Rust
toolchain, or in Docker when given `ORACLE=docker` (for example
`make interop ORACLE=docker`).

## Roadmap

See [ROADMAP.md](ROADMAP.md) for planned work, including further
performance investigation.

## Notes

- RFC 6330 has intellectual property disclosures on file with the IETF,
  including from Qualcomm. Review them before deploying.
- Credits:
  - The first known-answer vectors come from github.com/takeyourhatoff/raptorq (MIT), which generated them with cberner/raptorq.
  - The golden vectors come from cberner/raptorq (Apache-2.0).
