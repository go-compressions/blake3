//go:build (arm64 || amd64 || loong64 || riscv64 || ppc64le || s390x) && !goexperiment.simd

package blake3

import (
	"math/rand"
	"testing"
)

// TestMix4 checks the NEON kernel reproduces the scalar 7-round mixing for every
// lane, over many random states/messages.
func TestMix4(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 200; trial++ {
		var st, m [16][4]uint32
		var want [4][16]uint32
		for lane := 0; lane < 4; lane++ {
			var s0, mm [16]uint32
			for i := 0; i < 16; i++ {
				s0[i] = rng.Uint32()
				mm[i] = rng.Uint32()
				st[i][lane] = s0[i]
				m[i][lane] = mm[i]
			}
			for r := 0; r < 7; r++ {
				round(&s0, &mm, r)
			}
			want[lane] = s0
		}
		mix4(&st, &m)
		for lane := 0; lane < 4; lane++ {
			for i := 0; i < 16; i++ {
				if st[i][lane] != want[lane][i] {
					t.Fatalf("trial %d lane %d word %d: got %#08x want %#08x",
						trial, lane, i, st[i][lane], want[lane][i])
				}
			}
		}
	}
}

func BenchmarkMix4(b *testing.B) {
	var st, m [16][4]uint32
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 16; i++ {
		for l := 0; l < 4; l++ {
			st[i][l] = rng.Uint32()
			m[i][l] = rng.Uint32()
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mix4(&st, &m)
	}
}

// BenchmarkMix4Scalar is the same work done by the scalar kernel (4 lanes), for
// a like-for-like comparison.
func BenchmarkMix4Scalar(b *testing.B) {
	var s [4][16]uint32
	var m [4][16]uint32
	rng := rand.New(rand.NewSource(2))
	for l := 0; l < 4; l++ {
		for i := 0; i < 16; i++ {
			s[l][i] = rng.Uint32()
			m[l][i] = rng.Uint32()
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for l := 0; l < 4; l++ {
			for r := 0; r < 7; r++ {
				round(&s[l], &m[l], r)
			}
		}
	}
}

// TestFillChunkCVsASM checks the end-to-end NEON chunk path (compress4 chaining
// 16 blocks via mix4) matches the scalar path bit-for-bit, across batch and
// remainder sizes. The scalar path is validated against the official BLAKE3
// vectors elsewhere, so matching it proves the SIMD path correct end-to-end.
func TestFillChunkCVsASM(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, nChunks := range []int{4, 5, 7, 8, 13, 16} {
		data := make([]byte, nChunks*chunkLen)
		rng.Read(data)
		got := make([][8]uint32, nChunks)
		want := make([][8]uint32, nChunks)
		fillChunkCVs(data, got)
		fillChunkCVsScalar(data, want)
		for i := 0; i < nChunks; i++ {
			if got[i] != want[i] {
				t.Fatalf("nChunks=%d chunk %d: got %v want %v", nChunks, i, got[i], want[i])
			}
		}
	}
}

func BenchmarkFillChunkCVsASM(b *testing.B) {
	data := make([]byte, 1024*chunkLen) // 1 MiB
	rand.New(rand.NewSource(8)).Read(data)
	cvs := make([][8]uint32, 1024)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fillChunkCVs(data, cvs)
	}
}

func BenchmarkFillChunkCVsScalar(b *testing.B) {
	data := make([]byte, 1024*chunkLen)
	rand.New(rand.NewSource(8)).Read(data)
	cvs := make([][8]uint32, 1024)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fillChunkCVsScalar(data, cvs)
	}
}
