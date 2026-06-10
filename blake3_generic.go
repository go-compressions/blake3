//go:build !arm64 && !amd64 && !loong64 && !riscv64

package blake3

// fillChunkCVs (pure-Go) computes each full chunk's chaining value across CPU
// cores. This is the default build; it compiles whenever the experimental
// amd64 SIMD path (blake3_simd_amd64.go) is not selected, so the standard
// `go build` is unaffected by the SIMD code.
func fillChunkCVs(data []byte, cvs [][8]uint32) {
	fillChunkCVsScalar(data, cvs)
}
