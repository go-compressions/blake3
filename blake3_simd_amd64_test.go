//go:build goexperiment.simd && amd64

package blake3

import (
	"simd/archsimd"
	"testing"
)

// TestSIMDCompress8MatchesScalar directly cross-checks the 8-way SIMD chunk
// kernel against the scalar chunkCV, at two base offsets to exercise per-lane
// chunk counters. The whole-input paths (Sum256/Sum512) are covered by the
// shared official-vector tests, which route through fillChunkCVs in this build.
func TestSIMDCompress8MatchesScalar(t *testing.T) {
	t.Logf("AVX2=%v", archsimd.X86.AVX2())
	if !archsimd.X86.AVX2() {
		t.Skip("CPU lacks AVX2; SIMD kernel not exercised")
	}
	data := patternInput(16 * chunkLen)
	got := make([][8]uint32, 16)
	compress8(data, 0, got) // chunks 0..7  (counters 0..7)
	compress8(data, 8, got) // chunks 8..15 (counters 8..15)
	for i := 0; i < 16; i++ {
		if want := chunkCV(data, i); got[i] != want {
			t.Fatalf("chunk %d: simd %x != scalar %x", i, got[i], want)
		}
	}
}
