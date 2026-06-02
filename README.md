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
several times faster on a single core (hand-written AVX2). This package
deliberately trades that for being **pure Go with zero assembly** — portable to
every `GOARCH` and trivially auditable.

## License

BSD-3-Clause. See [LICENSE](LICENSE).
