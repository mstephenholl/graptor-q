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
(arm64, s390x, 386), `lint` (golangci-lint v2.14), `interop`, `oracle-vectors`,
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
- `perf`, once it is calibrated (step 7 of "Going public"): the library
  is not slower than on `main`.

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
| `main` / `main`, 3 dispatches | noise of identical builds | TODO |
| `89a9be9^` / `89a9be9` | a neutral change that moves code | TODO |
| `2882e7c` / `2882e7c^` | a 6–16% slowdown of encode-cold | TODO |

The threshold is the smallest at which none of the neutral runs fails, plus
one percent, provided the known slowdown is still flagged. Repeat the
calibration when the suite changes or the check raises a false alarm.

A slowdown smaller than the threshold passes, and several of them add up.
Compare an older release with `main` from time to time with
`perf-calibrate.yml` (base = the release tag).

## Going public

Rulesets need a public repository on GitHub Free, and the `release` job
does nothing while the repository is private. The steps, in order:

1. While the repository is still private, merge the pull request that
   removes "private" from `README.md` and `ROADMAP.md`. pkg.go.dev keeps
   the README of every version, and no version is released before step 2.
2. Make the repository public:
   `gh repo edit mstephenholl/graptor-q --visibility public --accept-visibility-change-consequences`
3. Right after, require approval before workflows run for pull requests
   from outside contributors (the endpoint answers 422 while private):
   `gh api -X PUT repos/mstephenholl/graptor-q/actions/permissions/fork-pr-contributor-approval -f approval_policy=all_external_contributors`
4. Create the labels and the rulesets: `.github/rulesets/apply.sh`. It
   prints the rulesets GitHub stored, and fails unless you can always
   bypass each of them.
5. Release v0.1.0: `gh workflow run ci --ref main`, or merge a pull
   request. Re-running an older run of `main` releases nothing, because a
   re-run reuses its event, which says the repository is private.
6. Calibrate the perf check (above), fill in the table, and set the
   `-threshold` default of `perfgate verdict` in
   `internal/cmd/perfgate/main.go` in a pull request.
7. Require the perf check: add `perf` to the required checks in
   `.github/rulesets/main.json` in a pull request, merge it, and run
   `.github/rulesets/apply.sh`.
