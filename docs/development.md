# Development

## Repository layout

| Path | Contents |
|---|---|
| `*.go` | Public API: OTI, Payload ID, §4.3 derivation, layout, block and object encoders and decoders, plan cache |
| `internal/gf256` | GF(256) arithmetic and kernels (generic, SSSE3, AVX2, GFNI, NEON) |
| `internal/rfc` | Table 2, Rand, Deg, Tuple, row patterns. `gentables` generates `tables_gen.go` from the RFC text |
| `internal/solver` | Symbolic inactivation decoding, the plan, and the §5.8 statistics test |
| `internal/refsolve` | Dense reference implementation (test oracle only) |
| `internal/cmd/release` | CI tool: tags each commit of main with the next version |
| `internal/cmd/perfgate` | CI tool: compares two builds' benchmarks on one machine |
| `interop/` | Separate module: differential tests against xssnick, takeyourhatoff and raptorgo, live cberner tests, comparison benchmarks |
| `tools/rqoracle` | Rust oracle built on cberner/raptorq 2.0.1: golden vectors, encoding and decoding for the interop tests, and benchmarks. Builds with Cargo or, without a Rust toolchain, with its Dockerfile |

## Make targets

The main make targets are `test`, `test-purego`, `test-race`, `test-cross`
(arm64, s390x, riscv64, 386), `lint` (golangci-lint v2.14), `interop`, `oracle-vectors`,
`bench-compare`, `stat-long`, and `fuzz`. The targets that need the oracle build it with the local Rust
toolchain, or in Docker when given `ORACLE=docker` (for example
`make interop ORACLE=docker`).

## Pull requests

`main` accepts changes only through squash-merged pull requests whose
required checks pass (`.github/rulesets/main.json`):

- `ci`: every job of `ci.yml` passed.
- `release-plan`: the release labels are valid and high enough for the
  change to the exported API; the job summary shows the version the merge
  will release.
- `perf`: the library is not slower than on `main`.

The repository owner can bypass every rule: merge without the checks, or
push to `main` directly.

## Releases

Every commit that reaches `main` and passes CI is tagged with the next
version and gets a GitHub release, by the `release` job of `ci.yml`. The
level comes from one label on the pull request:

| Label | v0 (now) | v1 and later |
|---|---|---|
| none or `release:patch` | v0.y.z+1 | vX.y.z+1 |
| `release:minor` | v0.y+1.0, also for breaking changes | vX.y+1.0 |
| `release:major` | v1.0.0: the compatibility promise | vX+1.0.0, which needs `module github.com/mstephenholl/graptor-q/vX+1` in go.mod in the same pull request |

`release-plan` also compares the exported API of the base branch and the
pull request with `apidiff` (`golang.org/x/exp/cmd/apidiff`). When it
reports incompatible changes, the check fails unless the pull request is
labeled `release:minor` or `release:major` in v0, or `release:major` from
v1 on.

The first release is v0.1.0. A release covers every commit since the last
tag and takes the highest level among them, so a commit whose release did
not run (failed CI, a cancelled job) is released with the next one. To
release without a merge, run the `ci` workflow on `main`
(`gh workflow run ci --ref main`). Tags are never moved or deleted: the Go
module proxy and checksum database keep the first content they see for a
version.

## Perf check

The `perf` check (`perf.yml`) builds `BenchmarkGate` (`gate_bench_test.go`)
from the pull request and from `main`, and runs the two builds alternately on
one core of the same runner: 20 rounds, each running every case once per
side for 200 ms, in alternating order. A case fails when the median of its
20 per-round time ratios is more than the threshold slower and the head was
slower in at least 15 of the 20 rounds (the 95% sign-test interval of the
median is above zero). The job summary shows every case. When the two
test binaries, built with `-trimpath`, are byte-identical, nothing is
measured: a change to documentation, workflows or `interop/` moves no
compiled code.

A pull request runs its own copy of the workflows and of `internal/cmd`, so
it can weaken its own checks. Review changes there before merging.

To accept a regression, add the label `perf:accept` (it counts only when the
repository owner adds it) and re-run the failed jobs of the run. Only the
`perf` job runs again, on the measurements already taken.

### Calibration

The threshold is the default of `perfgate verdict -threshold`
(`internal/cmd/perfgate`), changed there after calibration. It is chosen
with `perf-calibrate.yml`, which runs the same comparison on 10 runners at
once:

| base / head | what it measures | result |
|---|---|---|
| `main` / `main`, 3 dispatches | noise of identical builds | 30 runs: largest median +3.3% (encode, K=50000) |
| `calib/neutral-base` / `calib/neutral-head` (tags) | a neutral change that moves the kernels: #9's code change, with the loops aligned on both sides | 10 runs: largest median +3.9% (encode, K=50000) |
| `2882e7c` / `2882e7c^` | a known slowdown: reverting the `NewPlan` scratch pool | flagged in 10 of 10 runs: encode-cold +14.0% (K=100) and +11.0% (K=1000), median of medians |

Dispatch each pair with `gh workflow run perf-calibrate -f base=<base> -f head=<head>`.
The threshold is the smallest at which none of the neutral runs fails, plus
one percent, provided the known slowdown is still flagged. If that takes
more than 10%, raise `-rounds` before requiring the check: a weaker gate
would miss the known slowdown at K=1000. Repeat the
calibration when the suite changes or the check raises a false alarm.

On 2026-10-02 the 40 neutral runs saw six CPU models and none failed at 4%
or more (2 failed at 2%, 1 at 3%), so the threshold is 5%. The known
slowdown was flagged at every threshold up to 10% and at none at 15%.

The amd64 kernels start every loop at a 64-byte boundary (`PCALIGN $64`).
Without it, a change anywhere in the binary can move a loop within its cache
line: on three GitHub runners, two builds with identical kernels differed
by up to 26.6%, and by at most 4.2% with the alignment.

A slowdown smaller than the threshold passes, and several of them add up.
Compare an older release with `main` from time to time with
`perf-calibrate.yml` (base = the release tag).

## Repository settings

The repository went public on 2026-10-02, and released v0.1.0. It relies
on these settings:

- `.github/rulesets/main.json`: pull requests only, squash merges only,
  the required checks above, no force push or deletion of `main`.
  `.github/rulesets/release-tags.json`: release tags cannot be moved or
  deleted. Your account and the Admin role bypass both. After editing
  either file, run `.github/rulesets/apply.sh` as the owner. It prints the
  rulesets GitHub stored, and fails unless you can always bypass each.
- Workflows from pull requests of outside contributors wait for approval
  (`all_external_contributors`).
- Merged branches are deleted. A squash commit takes the pull request's
  title and description.
