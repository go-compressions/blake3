# Stable-Go SIMD BLAKE3 for all four targets, via go-asmgen

`blake3_simd_{amd64,arm64}.go` accelerate BLAKE3 with Go's **experimental**
`archsimd` intrinsics (`//go:build goexperiment.simd`, **Go 1.27+**,
`GOEXPERIMENT=simd`). On a plain `go build` none of it applies and BLAKE3 runs
scalar. This branch gives the same SIMD on **stable Go, no build flags**, on
**all four 64-bit Go targets**, generated with
[go-asmgen](https://github.com/go-asmgen/asmgen).

`mix4` is the 7-round, 4-lane BLAKE3 mixing kernel (4 chunks in parallel, one per
32-bit lane). `compress4ASM` chains a chunk's 16 blocks through it; `fillChunkCVs`
(now the default on these arches) hashes four full chunks per batch across cores,
scalar tail for the remainder. One generator per arch — same gVec/round/schedule
structure, different instructions — all Add/Xor/rotate, no multiply.

| arch | ISA | generator | rotate | registers | validated |
|---|---|---|---|---|---|
| arm64   | NEON  | `mix4_neon_gen.go` | shift-or            | 32 → state stays in regs | native (Apple Silicon) |
| amd64   | SSE2  | `mix4_sse_gen.go`  | `PSHUFLW`/shift-or  | 16 → state in memory     | native (GitHub CI)     |
| loong64 | LSX   | `mix4_lsx_gen.go`  | `VROTRW` (native)   | 32 (memory-resident)     | qemu `la464`           |
| riscv64 | RVV   | `mix4_rvv_gen.go`  | shift-or            | 32, `VSETVLI` VL=4       | qemu `rv64,vlen=128`   |

## Correctness

Every arch: `mix4` matches the scalar `round()`×7 per lane (`TestMix4`), the
chunk path matches `fillChunkCVsScalar` (`TestFillChunkCVsASM`), and the **full
official BLAKE3 test vectors** (inputs to 1 MiB) pass through the SIMD path.
arm64/amd64 run natively; riscv64/loong64 are cross-compiled and run under qemu:

Containers are Debian (glibc, reproducible), never alpine/busybox:

```sh
docker run --privileged --rm tonistiigi/binfmt --install all
# riscv64 (RVV needs the V extension + VLEN>=128), on Debian Trixie:
CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 go test -c -o /tmp/t .
docker run --rm --platform linux/riscv64 -e QEMU_CPU=rv64,v=true,vlen=128 -v /tmp:/t debian:trixie /t/t
# loong64 (LSX needs la464). loongarch64 is not yet in the official debian Docker
# manifest, so use the from-source Debian loong64 from loong13.debian.net (mirror
# mirrors.loong64.com, debian-loong64 archive keyring):
CGO_ENABLED=0 GOOS=linux GOARCH=loong64 go test -c -o /tmp/t .
docker run --rm --platform linux/loong64 -e QEMU_CPU=la464 -v /tmp:/t ghcr.io/loong64/debian:trixie /t/t
```

The same matrix runs in CI (`.github/workflows/simd-4arch.yml`): native amd64 +
arm64, plus these two Debian qemu jobs. RVV uses shift-or rotates because Go's
assembler has no `vror.vi` (the Zvbb bit-manip rotate); LSX gets a native
`VROTRW`.

## Performance (native arches; emulated arches measure correctness only)

`mix4` (4 lanes, one call) and `fillChunkCVs` (1 MiB, parallel across cores):

| arch | kernel scalar→SIMD | end-to-end scalar→SIMD |
|---|---|---|
| arm64 (16-core M) | ~759 → ~197 ns (**~3.85x**) | scalar→SIMD **~3x** |
| amd64 (4-core CI) | ~1158 → ~200 ns (**~5.8x**) | ~432 → ~1875 MB/s (**~4.3x**) |

A `sync.Pool` for the per-batch state/message planes (they escape to the heap
through the asm call) cut allocations from ~519 to **19 per 1 MiB hash**.

All on plain `go build`. The win is real per-core and survives multi-core
parallelism. The take-away: go-asmgen brings SIMD BLAKE3 to **stable Go, every
64-bit target, today** — where the intrinsics path needs an unreleased Go.
