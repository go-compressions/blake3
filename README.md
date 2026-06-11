# blake3

[![ci](https://github.com/go-compressions/blake3/actions/workflows/ci.yml/badge.svg)](https://github.com/go-compressions/blake3/actions/workflows/ci.yml)
![coverage](https://img.shields.io/badge/coverage-100%25-brightgreen)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-compressions/blake3.svg)](https://pkg.go.dev/github.com/go-compressions/blake3)

Pure-Go, cgo-free implementation of the **BLAKE3** cryptographic hash (default,
unkeyed mode), verified against the official BLAKE3 test vectors. Single static
dependency-free package; 100% test coverage.

```go
import "github.com/go-compressions/blake3"

sum := blake3.Sum256(data)            // [32]byte

h := blake3.New()                     // streaming (hash.Hash-style)
h.Write(part1); h.Write(part2)
digest := h.Sum(nil)                  // 32-byte digest

var xof [131]byte                     // extendable output (XOF)
h.Digest(xof[:])
```

## API

- `Sum256(data) [32]byte`, `Sum512(data) [64]byte`
- `New() *Hasher` with `Write`, `Sum(b) []byte`, `Reset`, `Size`, `BlockSize`,
  and `Digest(out []byte)` for arbitrary-length extendable output.

Default (unkeyed) hash mode only; keyed and derive-key modes are not implemented.

## Performance

Two pure-Go (no assembly, no cgo) optimisations are applied:

- **Precomputed message schedule** — the per-round message-word permutation is
  folded into a `[7][16]` lookup table at init, so the compression function
  indexes the original message directly instead of physically permuting a
  16-word array six times per block.
- **Multi-core one-shot hashing** — BLAKE3's chunk tree makes per-chunk
  chaining values independent, so `Sum256`/`Sum512` compute them across all CPU
  cores (`runtime.NumCPU()`); the cheap `O(chunks)` tree merge stays sequential.
  Inputs below 16 chunks (16 KiB) are hashed inline to avoid goroutine overhead.
  The result is byte-identical to the streaming `Hasher`.

Representative throughput on a 16-core machine (`Sum256`):

| Input | This package | Notes |
| --- | --- | --- |
| 2 KiB | ~0.35 GB/s | single chunk — scalar path only |
| 64 KiB | ~0.9 GB/s | parallel kicks in |
| 1 MiB | ~1.8 GB/s | |
| 16 MiB | ~2.1 GB/s | scales with cores |

The streaming `Hasher` (`Write`/`Sum`/`Digest`) is single-threaded by design;
use `Sum256`/`Sum512` for one-shot hashing of large buffers to get the parallel
speed-up.

An SIMD-assembly implementation such as
[`lukechampine.com/blake3`](https://github.com/lukechampine/blake3) is still
several times faster on a single core (hand-written AVX2). The default build of
this package deliberately trades that for being **pure Go with zero assembly** —
portable to every `GOARCH` and trivially auditable.

### Experimental SIMD path (amd64, `GOEXPERIMENT=simd`)

There is now a **cgo-free SIMD acceleration that is still pure Go** — no
assembly. It uses Go 1.26's experimental [`simd/archsimd`] intrinsics and only
compiles under `GOEXPERIMENT=simd` on `amd64` (`blake3_simd_amd64.go`, guarded
by `//go:build goexperiment.simd && amd64`). Every other build is byte-for-byte
unaffected and falls back to the pure-Go kernel.

It hashes **8 full chunks at once**, one chunk per 256-bit AVX2 lane: the 16
compression-state words become 16 vectors and the quarter-round is pure
elementwise `Add`/`Xor` plus a shift-or rotate (kept inside AVX2 — `archsimd`'s
`RotateAllRight` would lower to AVX-512). The result is **bit-identical** to the
scalar path, verified against the official BLAKE3 vectors in the `simd` CI job.

```sh
GOEXPERIMENT=simd go test ./...      # requires Go 1.26+, amd64 with AVX2
```

There is a matching **arm64 NEON** kernel (`blake3_simd_arm64.go`, guarded by
`//go:build goexperiment.simd && arm64`) using the same intrinsics. NEON
registers are 128-bit, so it hashes **4 chunks per lane** (`Uint32x4`). arm64
support for `simd/archsimd` landed on Go's `dev.simd` branch (targeting Go
1.27), so this path needs a Go 1.27+ toolchain; on Go 1.26 it's simply not
compiled. Verified bit-identical to scalar against the official vectors,
natively on an Apple M4 Max:

| input | scalar | archsimd NEON | Δ |
| --- | --- | --- | --- |
| 2 KiB | 337 MB/s | 335 MB/s | tie (single chunk — no SIMD) |
| 64 KiB | 841 MB/s | 841 MB/s | tie (scalar fallback — see below) |
| 1 MiB | 1791 MB/s | 2452 MB/s | **+37%** |
| 16 MiB | 2350 MB/s | 3297 MB/s | **+40%** |

The win is real but modest because the scalar path is already multi-core and
large inputs are memory-bandwidth bound; SIMD only adds per-core width on top.
Crucially it is a *win* — the same kernel built on the function-call SIMD
library go-highway was 8–40× *slower* (see above). That is the whole point:
inlined compiler intrinsics suit a fixed-size kernel; function-call wrappers do
not.

The kernel **only switches to SIMD once there are at least `NumCPU` chunk
batches** — enough to fill the cores and amortize the per-batch word transpose.
Below that the scalar per-chunk path (more, lighter tasks) is faster, so the
SIMD build is never slower than scalar at any size. The crossover was measured
exactly on the 16-core M4 Max: scalar wins at 64 KiB (15 batches), NEON wins
from 80 KiB (19 batches) — i.e. right at batches ≈ NumCPU. The same gate guards
the amd64 path.

Caveats, by design: it's **experimental** (the `simd` package is outside the Go
1 compatibility promise), needs a **non-default build flag**, and is **amd64
(Go 1.26+) / arm64 (Go 1.27+)** only — other arches keep the pure-Go path. Opt-in,
never the default.

[`simd/archsimd`]: https://pkg.go.dev/simd/archsimd

### Stable-Go SIMD on six 64-bit arches (go-asmgen, no build flag)

The experimental `archsimd` path above needs an unreleased Go and only covers
amd64/arm64. There is also a **default-build** SIMD path (plain `go build`, no
`GOEXPERIMENT`) that runs on **all six 64-bit Go targets** — amd64 (SSE2),
arm64 (NEON), riscv64 (RVV), loong64 (LSX), **ppc64le (VSX)**, and **s390x
(vector facility)**. The 7-round 4-lane BLAKE3 mixing kernel `mix4` (4 chunks in
parallel, one per 32-bit lane — pure Add/Xor/rotate, no multiply) is generated
per-arch with [go-asmgen]; the `.s` files are committed, so the module keeps
zero dependencies. Each arch is gated `<arch> && !goexperiment.simd` and falls
back to the scalar pure-Go kernel everywhere else. Details in
[`BLAKE3_ASMGEN_NOTES.md`](BLAKE3_ASMGEN_NOTES.md).

Every arch is verified **bit-identical to the scalar path against the official
BLAKE3 vectors** (inputs to 1 MiB): amd64/arm64 natively, the other four
cross-compiled and run under qemu on Debian in CI
([`simd-6arch.yml`](.github/workflows/simd-6arch.yml)). s390x is **big-endian**,
so its vectors passing proves the digest is endianness-independent (the
little-endian BLAKE3 message words are decoded with `binary.LittleEndian`,
matching the scalar load; the per-word vector arithmetic needs no byte-swap).

| arch | ISA | rotate | status |
| --- | --- | --- | --- |
| arm64   | NEON | shift-or | native, ~3.85× kernel speedup |
| amd64   | SSE2 | `PSHUFLW`/shift-or | native, ~5.8× kernel speedup |
| loong64 | LSX  | `VROTRW` | qemu-validated bit-exact; native perf pending |
| riscv64 | RVV  | shift-or | qemu-validated bit-exact; native perf pending |
| ppc64le | VSX  | `VRLW` (rotl 32−n) | qemu-validated bit-exact; native perf pending |
| s390x   | z-vec | `VERLLF` (rotl 32−n) | qemu-validated bit-exact; native perf pending |

[go-asmgen]: https://github.com/go-asmgen/asmgen

### Why not a portable SIMD library (go-highway, kelindar/simd, …)

We evaluated [`go-highway`] — a "write SIMD once, run on AVX2/NEON/fallback"
library — as a way to get SIMD on **arm64** too (Go's `archsimd` is amd64-only
for now). A NEON 4-way chunk kernel was implemented and verified bit-identical
to the scalar path, then benchmarked against the scalar build on the same
machine.

`BenchmarkSum256`, Apple M4 Max (16 cores), Go 1.26, go-highway v0.0.12:

| input | scalar (pure-Go) | go-highway NEON | result |
| --- | --- | --- | --- |
| 2 KiB | 346 MB/s | 339 MB/s | tie (single chunk — no SIMD either way) |
| 64 KiB | 863 MB/s | **22 MB/s** | ~40× slower |
| 1 MiB | 1760 MB/s | 232 MB/s | ~7.6× slower |
| 16 MiB | 2273 MB/s | 240 MB/s | ~9.5× slower |

So we **do not use it**. The cause is structural, not specific to go-highway:
it (like `kelindar/simd`, `vek`, and similar) exposes each SIMD op as a
**non-inlined assembly call that passes vectors through memory**. Those
libraries are built for *bulk elementwise math over large arrays* (one asm call
amortized over thousands of elements). BLAKE3's inner loop is the opposite — a
fixed-size kernel of thousands of tiny **interdependent** vector ops — so the
per-call + memory-round-trip overhead dwarfs the actual 16/32-byte SIMD work.

The right tool for a kernel like this is **inlined, register-allocated compiler
intrinsics** — i.e. Go's `simd/archsimd` (above), or hand-written whole-kernel
assembly (e.g. [`lukechampine.com/blake3`], not pure Go). The portable SIMD
*libraries* cannot inline, so they lose to scalar here. When `archsimd` gains
arm64 (NEON/SVE) support, the `compress8` kernel extends to arm64 in the same
style — that is the path we'll take, rather than a function-call library.

Reproduce: `go test -run x -bench BenchmarkSum256 -benchtime=1s ./...` (scalar)
versus the same with the go-highway kernel (the spike is not committed; see this
section's git history / the commit that added `BenchmarkSum256`).

[`go-highway`]: https://github.com/ajroetker/go-highway
[`lukechampine.com/blake3`]: https://github.com/lukechampine/blake3

## License

BSD-3-Clause. See [LICENSE](LICENSE).
