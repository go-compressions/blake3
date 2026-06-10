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

## End-to-end: full chunk hashing on stable Go

`compress4ASM` chains a chunk's 16 blocks through `mix4`; `fillChunkCVsASM`
hashes four full chunks per NEON batch (parallelised across cores like the
archsimd path) and falls back to the scalar per-chunk path for the tail.
Bit-identical to `fillChunkCVsScalar` for every size tested (4..16 chunks,
batches + remainder), so it produces correct BLAKE3 hashes end-to-end.

Throughput, 1 MiB, **both paths parallel across 16 cores** (Apple Silicon):

| | MB/s |
|---|---|
| scalar `fillChunkCVs` | ~1140 |
| **NEON `fillChunkCVs`** | **~3300** |

**~2.9x end-to-end**, on plain `go build` — the per-block ~3.85x carries through
to wall-clock even when both are already multi-core. (The asm path still does a
couple of escaping allocations per batch — `&st`/`&m` to the asm call — worth a
buffer pool later; the win stands regardless.)

## Footnote: the arm64 NEON integer-multiply gap is trivially closable

BLAKE3 needs no multiply, but Adler-style kernels do, and Go's arm64 assembler
lacks integer `VMUL`. Prototyped adding it: **~10 lines across 3 files**
(`a.out.go` opcode, `anames.go` name, `asm7.go` optab + `case 72` + `oprrr` bits
`0x0E209C00`), built `cmd/asm`, and verified by two disassemblers that
`VMUL V1.S4, V2.S4, V3.S4` encodes to `0x4ea19c43` = `mul v3.4s, v2.4s, v1.4s`
(all six arrangements correct, `.2D` rejected). So the gap is a small upstream
`cmd/asm` CL, not a design limit.
