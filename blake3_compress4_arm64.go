//go:build arm64 && blake3asm

package blake3

import "unsafe"

// compress4ASM computes the chaining values of the four full chunks
// base..base+3 using the NEON mix4 kernel (4 chunks in parallel, one per lane),
// writing them into cvs. Each chunk's 16 blocks are chained exactly as the
// scalar path does, but four chunks at a time.
func compress4ASM(data []byte, base int, cvs [][8]uint32) {
	// st and m are hoisted out of the block loop and reused: each is passed by
	// pointer to mix4 (asm), which forces them to escape, so allocating once per
	// batch instead of once per block avoids 16x the garbage.
	var st, m [16][4]uint32
	var cv [8][4]uint32
	for i := 0; i < 8; i++ {
		for l := 0; l < 4; l++ {
			cv[i][l] = iv[i]
		}
	}
	var ctrLo, ctrHi [4]uint32
	for l := 0; l < 4; l++ {
		c := uint64(base + l)
		ctrLo[l] = uint32(c)
		ctrHi[l] = uint32(c >> 32)
	}

	for b := 0; b < 16; b++ {
		// Transpose: m[i] lane l = word i of block b of chunk base+l. The block
		// bytes are the little-endian words on this (LE) arch, matching the
		// scalar's binary.LittleEndian load.
		for l := 0; l < 4; l++ {
			off := (base+l)*chunkLen + b*blockLen
			w := unsafe.Slice((*uint32)(unsafe.Pointer(&data[off])), 16)
			for i := 0; i < 16; i++ {
				m[i][l] = w[i]
			}
		}

		var flags uint32
		if b == 0 {
			flags = flagChunkStart
		}
		if b == 15 {
			flags |= flagChunkEnd
		}

		for l := 0; l < 4; l++ {
			st[0][l], st[1][l], st[2][l], st[3][l] = cv[0][l], cv[1][l], cv[2][l], cv[3][l]
			st[4][l], st[5][l], st[6][l], st[7][l] = cv[4][l], cv[5][l], cv[6][l], cv[7][l]
			st[8][l], st[9][l], st[10][l], st[11][l] = iv[0], iv[1], iv[2], iv[3]
			st[12][l], st[13][l], st[14][l], st[15][l] = ctrLo[l], ctrHi[l], blockLen, flags
		}

		mix4(&st, &m)

		for i := 0; i < 8; i++ {
			for l := 0; l < 4; l++ {
				cv[i][l] = st[i][l] ^ st[i+8][l]
			}
		}
	}

	for i := 0; i < 8; i++ {
		for l := 0; l < 4; l++ {
			cvs[base+l][i] = cv[i][l]
		}
	}
}

// fillChunkCVsASM computes every chunk's chaining value, hashing four full
// chunks at a time through the NEON kernel and falling back to the scalar
// per-chunk path for the tail (and any partial final chunk). It produces
// bit-identical results to fillChunkCVsScalar.
func fillChunkCVsASM(data []byte, cvs [][8]uint32) {
	n := len(cvs)
	// Count the leading full 4-chunk batches that lie entirely within data.
	batches := 0
	for (batches+1)*4 <= n && (batches+1)*4*chunkLen <= len(data) {
		batches++
	}
	parallelChunks(batches, func(bi int) {
		compress4ASM(data, bi*4, cvs)
	})
	// Tail (and any partial final chunk) via the scalar per-chunk path.
	for i := batches * 4; i < n; i++ {
		cvs[i] = chunkCV(data, i)
	}
}
