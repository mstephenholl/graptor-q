# Validation

## Scope

graptor-q implements all of RFC 6330:

- Object Transmission Information (12-byte OTI, including erratum 5548).
- FEC Payload IDs.
- Source block and sub-block partitioning with symbol alignment and sub-symbol interleaving (§4.4).
- The §4.3 parameter derivation.
- Any K up to 56,403 per block, any ESI up to 2²⁴−1.
- Packets that carry several consecutive symbols, and source symbols without the padding at their end (§4.4.2). Decoders accept such symbols in `AddPacket` and `AddSymbol`, and reject a symbol that leaves out anything else. With sub-blocks, several symbols of the last source block can end with padding.

## Checks

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
| Platforms: amd64, `purego`, arm64 (natively in CI and under qemu), s390x (big-endian, qemu), riscv64 (qemu), 386 | Portability |

## Finding: xssnick/raptorq deviates from RFC 6330 at 99 of the 477 K'

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

## Credits

The first known-answer vectors come from github.com/takeyourhatoff/raptorq
(MIT), which generated them with cberner/raptorq. The golden vectors come
from cberner/raptorq (Apache-2.0).
