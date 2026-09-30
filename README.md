# graptorq

A pure Go implementation of RaptorQ, the fountain code of
[RFC 6330](https://www.rfc-editor.org/rfc/rfc6330). It covers the core codec
and the object-delivery layer, has no dependencies, and has SIMD kernels for
amd64 (AVX2, GFNI) and arm64 (NEON).

```go
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
- **Streaming for objects larger than memory.** `NewEncoderReaderAt` reads source blocks from an `io.ReaderAt` on demand; `WithBlockCache` bounds how many stay ready. `NewDecoderWriterAt` writes each block to an `io.WriterAt` as soon as it decodes, then frees its memory. `*os.File` works for both. Streaming a 64 MB file this way grows the heap by about 1.5 MB.
- **Block-level API.** `BlockEncoder` and `BlockDecoder` work on a single source block with any symbol size.
- **Packets and limits.**
  - `AppendSymbols` builds packets that carry several consecutive symbols (RFC 6330 §4.4.2), and `AppendRepair` generates the next n repair symbols of a block.
  - `AddPacket` accepts source packets that leave out the padding at the end of their last symbol, as §4.4.2 allows. With sub-blocks, several symbols of the last source block can end with padding. Leaving out anything other than padding is rejected.
  - For untrusted input, `WithMaxOverhead` caps the symbols stored per block and `WithMaxMemory` caps each block's memory.
- **Fast solver.**
  - Inactivation decoding is compiled into a straight-line *plan* of symbol operations.
  - Encoding plans depend only on K', so they are cached and reused across blocks.
  - Decoders compute only the intermediate symbols needed for the missing source symbols.
  - When only a few source symbols are missing, decoders skip the symbolic solve entirely. They reuse the cached encoding plan and solve a small m×m system for the m missing symbols. This is 1.3–2.8× faster than the full solver and is chosen by a measured cost model.
  - Decoders reuse all of their working memory: after `Reset`, decoding a block allocates nothing.
  - Large symbols are processed in parallel byte stripes.
- **Kernels.**

  | Tier | Selection | MulAdd throughput (16 KiB, one core) |
  |---|---|---|
  | GFNI (`VGF2P8AFFINEQB`) | amd64 with AVX2 and GFNI | 74 GB/s |
  | AVX2 (`VPSHUFB` nibble tables) | amd64 with AVX2 | 45 GB/s |
  | SSSE3 (`PSHUFB` nibble tables) | amd64 with SSSE3 but no AVX2 | 27 GB/s |
  | NEON (`TBL` nibble tables) | arm64 | — |
  | Generic Go | always; forced with `-tags purego` | 3 GB/s |

  The best supported tier is chosen at startup. `GRAPTORQ_GF256=<tier>` forces a tier for testing; `make test-tiers` runs the suite on each one.

  Note that `GF2P8MULB` cannot be used: it hard-codes the AES polynomial 0x11B, and RFC 6330 uses 0x11D.
- **No dependencies.** CPU features are detected with CPUID directly, because `x/sys/cpu` cannot report GFNI without AVX-512.

## Validation

| Check | What it establishes |
|---|---|
| Tables generated from the RFC text (`testdata/rfc6330.txt`, SHA-256 pinned) and re-checked on every test run | No transcription errors in V0–V3, Deg, Table 2, OCT_EXP/OCT_LOG |
| GF(256): all 65,536 products, quotients and inverses against the RFC's tables; every kernel tier against scalar code for all 256 constants, lengths 0–260 plus large sizes, and unaligned/aliased buffers; fuzzing | Field arithmetic and SIMD kernels |
| Dense reference solver (`internal/refsolve`): literal RFC loops, explicit MT×GAMMA product, scalar Gauss-Jordan | Independent oracle for the optimized solver |
| All 477 K' values: full rank, A·C = D residual check, systematic property | Constraint matrix construction |
| Differential against the reference solver on random received sets, including singular ones | Identical rank decisions and solutions |
| **cberner/raptorq 2.0.1 golden vectors** (`tools/rqoracle`, committed as `testdata/vectors`) | Byte-exact encoding symbols for 49 objects (3,938 symbols): Z up to 255, N up to 32 with TL≠TS, K up to 56,403, ESI 2²⁴−1. Also decoding of cberner packets, and identical §4.3 derivations |
| Live cberner interop (`make interop`) | Both directions, plus the first two repair symbols compared at **every K'** |
| **xssnick/raptorq v1.5.2 differential** (`interop/`) | Identical symbols at 378 of 477 K'; cross-decoding in both directions (see the finding below) |
| **takeyourhatoff/raptorq differential** (`interop/`) | Identical symbols at all 477 K', with and without its SIMD kernels. It derives from xssnick/raptorq but does not share its P1 deviation. Cross-decoding in both directions |
| **fgn/raptorgo v0.1.1 differential** (`interop/`), an independent implementation with the object layer | Identical §4.3 derivations for 52,608 inputs. Byte-identical packets, single and grouped, source and repair, for 20 objects with Z up to 5, N up to 13 and Al from 1 to 8. Cross-decoding of whole objects in both directions, including raptorgo's packets that leave out padding |
| §5.8 recovery properties, with ESIs uniform over 0..2²⁴−1 (`make stat-long`) | Failures at K' symbols: 491/10⁵ = 0.49% (bound 1%). At K'+1: 4/(3×10⁵) = 1.3×10⁻⁵ (bound 10⁻⁴). At K'+2: 0/10⁶ (bound 10⁻⁶) |
| Fuzzing: OTI parsing, garbage packets, block round trips | Robustness on untrusted input |
| Platforms: amd64, `purego`, arm64 under qemu (a native arm64 CI job is configured but has not run yet), s390x (big-endian, qemu), 386 | Portability |

### Finding: xssnick/raptorq deviates from RFC 6330 at 99 of the 477 K'

xssnick/raptorq v1.5.2 (`params.go`) computes P1 as the smallest prime *strictly
greater* than P:

```go
p._P1 = p._P + 1
for !isPrime(p._P1) { p._P1++ }
```

RFC 6330 §5.3.3.3 defines P1 as the smallest prime *greater than or equal to* P. The two
differ exactly when P is prime, the first case being K' = 49. At those sizes
its repair symbols differ from every RFC-compliant implementation (graptorq and
cberner agree there). Its encoder and decoder share the deviation, so it still
round-trips with itself, but it does not interoperate at those block sizes.
`interop/xssnick_test.go` documents this and fails if the behaviour changes.

## Performance

Single core (Intel Core Ultra 7 155U P-core, GOMAXPROCS=1), MB/s of source
data, median of 5 runs, Go 1.27.1.

- **Encode:** build the encoder for a new block, then generate one repair symbol. The solution procedure depends only on K', and two libraries can reuse it across blocks: graptorq through its plan cache (the default) and takeyourhatoff through `Encoder.Reset`. The "from scratch" rows solve every block anew (`WithoutPlanCache()` for graptorq, a new `Encoder` for takeyourhatoff), as xssnick, raptorgo and cberner always do.
- **Decode:** lose 10% of the source symbols, replace them with that many repair symbols plus two, decode, and deliver the block into a reused buffer. raptorgo cannot reuse a decoder, so its figures include creating one per block.
- **SIMD builds:** takeyourhatoff and raptorgo are measured with their SIMD kernels (AVX2 on this CPU; takeyourhatoff also has AVX-512 kernels), built with `GOEXPERIMENT=simd GOAMD64=v3`. raptorgo's need Go 1.26 and were measured with Go 1.26.5. Without the experiment they reach 43–153 MB/s (takeyourhatoff) and 7–63 MB/s (raptorgo). graptorq and xssnick do not use the experiment; their figures change by at most 5% under it.
- **raptorgo** only has an object API, so its figures use an OTI with a single source block. graptorq's object API is within 4% of its block API on the same benchmarks.

| | K | T | graptorq | xssnick v1.5.2 | takeyourhatoff | raptorgo v0.1.1 | cberner 2.0.1 |
|---|---:|---:|---:|---:|---:|---:|---:|
| encode, plan reused | 100 | 1280 | **2699** | – | 1483 | – | – |
| encode, plan reused | 1000 | 1280 | **2638** | – | 1398 | – | – |
| encode, plan reused | 10000 | 1280 | **1036** | – | 760 | – | – |
| encode, plan reused | 50000 | 256 | **525** | – | 452 | – | – |
| encode, from scratch | 100 | 1280 | 1434 | 889 | 800 | 263 | **1445** |
| encode, from scratch | 1000 | 1280 | **1332** | 776 | 703 | 185 | 1079 |
| encode, from scratch | 10000 | 1280 | **720** | 491 | 489 | 118 | 635 |
| encode, from scratch | 50000 | 256 | 221 | 111 | 113 | 19 | **337** |
| decode | 100 | 1280 | **2022** | 907 | 872 | 263 | 617 |
| decode | 1000 | 1280 | **1448** | 830 | 848 | 222 | 545 |
| decode | 10000 | 1280 | **705** | 472 | 495 | 113 | 369 |
| decode | 50000 | 256 | **215** | 107 | 117 | 13 | 75 |

The cberner figures come from an earlier run with its own benchmark (`rqoracle bench`).
Reproduce everything with `make bench-compare CPU=<a performance core>`; it
leaves out raptorgo's SIMD build, which does not compile with Go 1.27
(see `interop/raptorgo_simd_test.go`).

With `WithConcurrency(n)`, a large block is processed in parallel byte
stripes, which adds 40–75% for encode with 4 goroutines at T=1280.

## Repository layout

| Path | Contents |
|---|---|
| `*.go` | Public API: OTI, Payload ID, §4.3 derivation, layout, block and object encoder/decoder, plan cache |
| `internal/gf256` | GF(256) arithmetic and kernels (generic, AVX2, GFNI, NEON) |
| `internal/rfc` | Table 2, Rand, Deg, Tuple, row patterns. `tables_gen.go` is generated by `gentables` from the RFC text |
| `internal/solver` | Symbolic inactivation decoding, the plan, and the §5.8 statistics test |
| `internal/refsolve` | Dense reference implementation (test oracle only) |
| `interop/` | Separate module: differential tests against xssnick, takeyourhatoff and raptorgo, live cberner tests, comparison benchmarks |
| `tools/rqoracle` | Rust oracle built on cberner/raptorq 2.0.1: golden vectors, encode/decode for interop, benchmarks. Builds with Cargo or, without a Rust toolchain, with its Dockerfile |

Useful make targets: `make test`, `test-purego`, `test-race`, `test-cross`
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
