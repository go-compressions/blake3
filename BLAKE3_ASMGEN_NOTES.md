# Stable-Go SIMD BLAKE3 for all six targets, via go-asmgen

`blake3_simd_{amd64,arm64}.go` accelerate BLAKE3 with Go's **experimental**
`archsimd` intrinsics (`//go:build goexperiment.simd`, **Go 1.27+**,
`GOEXPERIMENT=simd`). On a plain `go build` none of it applies and BLAKE3 runs
scalar. This branch gives the same SIMD on **stable Go, no build flags**, on
**all six 64-bit Go targets**, generated with
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
| ppc64le | VSX   | `mix4_vsx_gen.go`  | `VRLW` (rotl 32-n)  | 32 (memory-resident)     | qemu (default POWER)   |
| s390x   | z-vec | `mix4_s390x_gen.go`| `VERLLF` (rotl 32-n)| 32 (memory-resident), **big-endian** | qemu (default z) |

`mix4_vsx_gen.go` (ppc64le) loads each 4-lane state word with `LXVD2X` and
operates per word with `VADDUWM`/`VXOR`. VSX has only rotate-**left** (`VRLW`),
so BLAKE3's `rotr(n)` is `rotl(32-n)`; the four count vectors {16,20,24,25} are
splatted once with `VSPLTISW` (`VRLW` masks the count to its low 5 bits, so the
in-range signed immediates -16/-12/-8/-7 splat to the right values — the same
trick `x/crypto/chacha20`'s ppc64 asm uses). Mind the **VSX↔VMX aliasing**: a
load feeding a `Vn` op must target `VS(32+n)`.

`mix4_s390x_gen.go` (s390x) is on a **big-endian** host but needs **no
byte-reversal inside `mix4`**: the state/message planes are Go `[16][4]uint32`,
so Go and the `VL`/`VST` 128-bit loads/stores agree on the big-endian word
interpretation, and `VAF`/`VX`/`VERLLF` operate per word on the correct numeric
values without ever moving a lane across another. The endianness that *does*
matter is the little-endian message-word decode from the raw BLAKE3 block bytes
— handled Go-side in `compress4ASM` with `binary.LittleEndian.Uint32` (matching
the scalar path), so the digest is bit-exact. The s390x qemu run passing the
official vectors is the proof.

## Correctness

Every arch: `mix4` matches the scalar `round()`×7 per lane (`TestMix4`), the
chunk path matches `fillChunkCVsScalar` (`TestFillChunkCVsASM`), and the **full
official BLAKE3 test vectors** (inputs to 1 MiB) pass through the SIMD path.
arm64/amd64 run natively; riscv64/loong64/ppc64le/s390x are cross-compiled and
run under qemu:

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
# ppc64le (VSX) and s390x (vector facility, big-endian) — the default qemu CPU
# model already has the vector unit, so no QEMU_CPU is needed:
CGO_ENABLED=0 GOOS=linux GOARCH=ppc64le go test -c -o /tmp/t .
docker run --rm --platform linux/ppc64le -v /tmp:/t debian:trixie /t/t
CGO_ENABLED=0 GOOS=linux GOARCH=s390x go test -c -o /tmp/t .
docker run --rm --platform linux/s390x -v /tmp:/t debian:trixie /t/t
```

The same matrix runs in CI (`.github/workflows/simd-6arch.yml`): native amd64 +
arm64, plus four Debian qemu jobs (riscv64/loong64/ppc64le/s390x). RVV uses
shift-or rotates because Go's assembler has no `vror.vi` (the Zvbb bit-manip
rotate); LSX/VSX/z-vec all have a native word rotate (`VROTRW`/`VRLW`/`VERLLF`).
ppc64le and s390x are **qemu-validated bit-exact** (including the official
vectors); native performance is pending hardware access.

## Performance (native arches; emulated arches measure correctness only)

`mix4` (4 lanes, one call) and `fillChunkCVs` (1 MiB, parallel across cores):

| arch | kernel scalar→SIMD | end-to-end scalar→SIMD |
|---|---|---|
| arm64 (16-core M) | ~759 → ~197 ns (**~3.85x**) | scalar→SIMD **~3x** |
| amd64 (4-core CI) | ~1158 → ~200 ns (**~5.8x**) | ~432 → ~1875 MB/s (**~4.3x**) |
| loong64/riscv64/ppc64le/s390x | qemu-validated bit-exact | native perf pending |

A `sync.Pool` for the per-batch state/message planes (they escape to the heap
through the asm call) cut allocations from ~519 to **19 per 1 MiB hash**.

All on plain `go build`. The win is real per-core and survives multi-core
parallelism. The take-away: go-asmgen brings SIMD BLAKE3 to **stable Go, every
64-bit target, today** — where the intrinsics path needs an unreleased Go.

## Regenerating the kernels

go-asmgen is a generate-time tool, not a module dependency (the `mix4_*_gen.go`
are `//go:build ignore`; the `.s` files are committed, so consumers build with no
extra deps and the module stays `go 1.26`). To regenerate after editing a
generator:

```sh
go get github.com/go-asmgen/asmgen@v0.5.0   # temporary, for the generators
go generate ./...                            # or: go run mix4_<arch>_gen.go
go mod tidy                                   # drops go-asmgen again
```
