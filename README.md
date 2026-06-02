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

## License

BSD-3-Clause. See [LICENSE](LICENSE).
