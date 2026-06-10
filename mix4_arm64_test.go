//go:build arm64 && blake3asm

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
