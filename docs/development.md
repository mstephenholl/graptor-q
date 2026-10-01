# Repository layout

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
