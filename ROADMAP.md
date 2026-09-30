# Roadmap

## Investigate further performance gains

**Status:** open. The initial target of 1.5–1.6× xssnick/raptorq on single-core
decode was met on 2026-09-30 (commit `125f26c`). Ratios by block size:

| K | T | Decode vs xssnick |
|---|---|---|
| 100 | 1280 | 2.25× |
| 1,000 | 1280 | 1.73× |
| 10,000 | 1280 | 1.48× |
| 50,000 | 256 | 2.00× |

Encode is 2.1–4.8×. The case that remains weakest is decoding mid-size blocks
with large symbols (K = 10,000, T = 1280). Run-to-run variation there is about ±3%.

### Where the time goes

Decoding K = 10,000, T = 1280 with 10% loss (about 18.7 ms) breaks down as:

| Share | Work |
|---|---|
| ~56% | Plan execution: fused XOR passes N1 and N4 over a working set of about 12.8 MB. This is memory-bandwidth-bound at about 15–20 GB/s effective, and the kernels themselves reach 70–100 GB/s on cache-resident data. |
| ~21% | Plan construction: about 400 ns per row, spread over phase 1, U-part bitsets, HDPC row reduction, assembly and row generation. |
| ~12% | The two copies the API requires: `AddSymbol` into the decoder and `AppendSource` out of it. xssnick makes the same two copies. |
| rest | Decoder bookkeeping and rebuilding the missing symbols. |

### Ideas not yet tried

- **Execution locality.** Reorder independent instructions, or relabel slots in execution order, so that operands are reused while they are still cached. Measure against the current order with `BenchmarkDecodePaths` and the decode-plan benchmarks.
- **Prune N1 when the HDPC rows are unused.** With enough extra repair symbols (more than about H), phase 2 needs no HDPC rows. Only the reduced right-hand sides reachable from the used binary rows are then needed, instead of all of them.
- **Prefetching for small symbols in large blocks.** A head-only prefetch of the next instruction's operands measured −12% at K = 50,000, T = 256. It had no effect at T = 1280 and cost 15% on cache-resident plans, so it needs a heuristic on T and the working-set size.
- **Zero-copy symbol ingestion.** An opt-in API where the caller hands over ownership of symbol buffers would remove one of the two copies, about 6% of decode.
- **Wider fused kernels.** A multi-source multiply-add for the HDPC phase-2 operations, and an AVX-512 tier on CPUs that have it.
- **Faster plan construction.** Every decode builds a plan (about 21% of decode time at K = 10,000), and so does an encoder without the plan cache. Construction is already 2–3.7× faster than cberner's (its `SourceBlockEncodingPlan::generate`), and pooling the scratch memory of `NewPlan` took another 11–15% off. In the profile at K' = 56,403, the largest single cost is expanding pivot bitsets into bytes for the HDPC recurrence, which costs more than the recurrence itself; a SIMD kernel that expands the bits in registers could remove it. Phase 1's per-row state (chosen, V-degree) lives in separate arrays, which costs two cache misses per row where one would do.
- **Parallel plan construction.** Relevant for multi-core decoders of large blocks, where plan construction is serial but execution already runs in parallel stripes.
- **Native arm64 profiling.** NEON correctness is verified under qemu, but its performance has never been measured.

### Already measured and rejected

- **Cache-blocked execution.** Replaying the plan over byte stripes was slower at every stripe width, for working sets from 1 MB to 64 MB (see `Plan.ExecuteRange`).
- **Software prefetching at T = 1280.** Whole-operand prefetch cost 13–20%; head-only prefetch was within ±1%.
- **Huge pages (`MADV_HUGEPAGE`).** No change at K = 10,000 and 2–3% at K = 50,000.
- **Bit-sliced HDPC recurrence.** Keeping z and the HDPC rows bit-sliced, so that adding the binary X_j is one XOR, was 4–5% slower up to K' = 10,017 and neutral at 56,403: AVX2 byte operations already handle 32 coefficients per instruction, and bit-slices need 8 XORs per 64.

### How to measure

- `make bench-compare CPU=<P-core>`: single-core comparison with xssnick and cberner.
- `BenchmarkDecodePaths`: the low-loss path against the full solver.
- `BenchmarkDecodePlan` in `internal/solver`: plan construction alone.

Pin to one performance core. On hybrid CPUs, hyperthread siblings make results noisy.

## AVX-512 kernel tier

**Status:** planned. The development machine (Intel Core Ultra 7 155U) has no
AVX-512, and QEMU cannot emulate it (it also hides GFNI), so the tier cannot
be tested natively yet.

**Expected benefit:** 64-byte vectors and `VPTERNLOGD` three-way XOR. The gain
will be largest on cache-resident work: the multiply-add kernels, the HDPC
step, and small blocks. It will be smaller on large decodes, which are limited
by memory bandwidth. EVEX-encoded GFNI on 512-bit registers comes for free.

**Plan:**
- **Kernels:** AVX-512 variants of xor, mul, mulAdd, the fused XOR/gather and the HDPC step, as a new tier. Select it with the CPUID leaf 7 AVX-512F and AVX-512BW bits, plus the XGETBV opmask/ZMM state bits (XCR0 bits 5–7).
- **Local validation with Intel SDE** (Software Development Emulator). Downloading it requires accepting Intel's end-user license agreement. Then run `go test -exec "sde64 -spr --" ./internal/gf256/ ./internal/solver/ .` with the tier forced (`GRAPTORQ_GF256=avx512`). SDE also emulates GFNI.
- **CI validation on GitHub-hosted runners**, which usually have AVX-512. Add a job that runs `make test-tiers` including the new tier; `TestEnvTier` reports it as skipped when the runner's CPU lacks it. Optionally also run the SDE job in CI, so coverage does not depend on the runner's CPU.
- **Hardware:** an AVX-512-capable development machine would allow native testing and benchmarking. Examples are AMD Zen 4/Zen 5 laptops and workstations, or cloud Xeon/EPYC instances.

## Other known gaps

- **CI:** the workflows in `.github/workflows` have not run yet. That includes the native arm64 job and the nightly statistics and fuzzing jobs.
- **Project:** choose a license; review the RFC 6330 IPR disclosures; report the xssnick P1 deviation upstream (see the README).
