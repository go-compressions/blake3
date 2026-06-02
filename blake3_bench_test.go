package blake3

import "testing"

// BenchmarkSum256 measures one-shot hashing throughput across sizes. Run it for
// the default (pure-Go) build and again with `-tags blake3hwy` (arm64 NEON) to
// compare the SIMD chunk kernel against the scalar one on the same machine.
func BenchmarkSum256(b *testing.B) {
	for _, size := range []int{2048, 65536, 1 << 20, 16 << 20} {
		in := patternInput(size)
		b.Run(sizeName(size), func(b *testing.B) {
			b.SetBytes(int64(size))
			for i := 0; i < b.N; i++ {
				_ = Sum256(in)
			}
		})
	}
}

func sizeName(n int) string {
	switch {
	case n >= 1<<20:
		return itoa(n>>20) + "MiB"
	case n >= 1<<10:
		return itoa(n>>10) + "KiB"
	default:
		return itoa(n) + "B"
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
