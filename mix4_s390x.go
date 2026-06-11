//go:build s390x && !goexperiment.simd

package blake3

func mix4(state *[16][4]uint32, m *[16][4]uint32)
