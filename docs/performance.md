# Performance

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
