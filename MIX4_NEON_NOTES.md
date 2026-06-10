# Experiment: stable-Go NEON BLAKE3 mixing via go-asmgen

`blake3_simd_arm64.go` accelerates BLAKE3 with Go's **experimental** `archsimd`
intrinsics — `//go:build goexperiment.simd && arm64`, needing **Go 1.27+** and
`GOEXPERIMENT=simd`. On a plain `go build` (stable Go) none of it applies and
BLAKE3 runs the scalar path.

This branch shows the same SIMD on **stable Go, no build flags**, via
[go-asmgen](https://github.com/go-asmgen/asmgen): `mix4_neon_gen.go` generates
`mix4_arm64.s` — the 7-round, 4-lane mixing kernel (4 chunks hashed in parallel,
one per uint32 lane), ~1200 NEON instructions.

- Pure `VADD` / `VEOR` / shift-or rotate — **no multiply**, so it is unaffected
  by the missing NEON integer multiply in Go's assembler.
- 16-word state kept live in V0..V15 across all rounds; message words loaded with
  128-bit `FMOVQ` at immediate offsets; one scratch register for the rotate.
- Build-tag `blake3asm`-gated, so default builds are untouched.

## Result (native arm64, Apple Silicon)

Correct: `mix4` matches the scalar `round()` applied 7× for every lane over 200
random states (`go test -tags blake3asm`).

| | ns/op (4 lanes) |
|---|---|
| scalar (`round`×7 ×4) | ~759 |
| **NEON `mix4`** | **~197** |

**~3.85x** — close to the 4x ceiling of 4-way SIMD. This is the per-block hot
core; wiring it into `fillChunkCVs` (message transpose + block loop in Go) would
carry the speedup end-to-end on stable Go, and the same generator approach ports
to amd64 SSE (and to other arches go-asmgen targets).
