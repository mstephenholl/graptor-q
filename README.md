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
- **Block-level API.** `BlockEncoder` and `BlockDecoder` work on a single source block with any symbol size.
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
  | AVX2 (`VPSHUFB` nibble tables) | amd64 with AVX2 | 32 GB/s |
  | NEON (`TBL` nibble tables) | arm64 | — |
  | Generic Go | always; forced with `-tags purego` | 3 GB/s |

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
data, median of 5 runs.

- **Encode:** build the encoder, then generate one repair symbol.
- **Decode:** lose 10% of the source symbols, replace them with that many repair symbols plus two, decode, and deliver the block into a reused buffer (`AppendSource` for graptorq, `DecodeInto` for xssnick).

| | K | T | graptorq | xssnick v1.5.2 | cberner 2.0.1 |
|---|---:|---:|---:|---:|---:|
| encode | 100 | 1280 | **2686** | 877 | 1445 |
| encode | 1000 | 1280 | **2544** | 748 | 1079 |
| encode | 10000 | 1280 | **1011** | 478 | 635 |
| encode | 50000 | 256 | **513** | 107 | 337 |
| decode | 100 | 1280 | **1972** | 878 | 617 |
| decode | 1000 | 1280 | **1374** | 795 | 545 |
| decode | 10000 | 1280 | **684** | 461 | 369 |
| decode | 50000 | 256 | **212** | 106 | 75 |

The cberner figures come from an earlier run with its own benchmark (`rqoracle bench`).
Reproduce everything with `make bench-compare CPU=<a performance core>`.

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
| `interop/` | Separate module: xssnick differential tests, live cberner tests, comparison benchmarks |
| `tools/rqoracle` | Rust oracle built on cberner/raptorq 2.0.1: golden vectors, encode/decode for interop, benchmarks |

Useful make targets: `make test`, `test-purego`, `test-race`, `test-cross`
(arm64, s390x, 386), `interop`, `oracle-vectors`, `bench-compare`, `stat-long`,
and `fuzz`.

## Roadmap

See [ROADMAP.md](ROADMAP.md) for planned work, including further
performance investigation.

## Notes

- RFC 6330 has intellectual property disclosures on file with the IETF,
  including from Qualcomm. Review them before deploying.
- Credits:
  - The first known-answer vectors come from github.com/takeyourhatoff/raptorq (MIT), which generated them with cberner/raptorq.
  - The golden vectors come from cberner/raptorq (Apache-2.0).
