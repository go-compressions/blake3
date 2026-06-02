//go:build goexperiment.simd && amd64

// This file is an EXPERIMENTAL, cgo-free SIMD acceleration of BLAKE3's
// per-chunk compression for amd64, written with Go 1.26's `simd` intrinsics
// package (no assembly). It only compiles under GOEXPERIMENT=simd && amd64;
// every other build uses the pure-Go fillChunkCVs in blake3_generic.go, so the
// default `go build` is byte-for-byte unaffected.
//
// Strategy: BLAKE3's chunk tree makes chunks independent, so we hash 8 full
// 1024-byte chunks at once — one chunk per 256-bit lane (Uint32x8). The 16
// compression state words become 16 vectors; the quarter-round mixing is pure
// elementwise Add/Xor/RotateAllRight with no cross-lane movement, which is
// exactly what the intrinsics expose. Results are bit-identical to the scalar
// path, verified against the official BLAKE3 test vectors under this build.

package blake3

import (
	"encoding/binary"
	"simd/archsimd"
)

// fillChunkCVs hashes 8 chunks per AVX2 batch, with the scalar chunkCV handling
// both the sub-8 remainder and CPUs without AVX2 (the intrinsics compile on any
// amd64 but the instructions must be present at runtime).
func fillChunkCVs(data []byte, cvs [][8]uint32) {
	n := len(cvs)
	if !archsimd.X86.AVX2() {
		parallelChunks(n, func(i int) { cvs[i] = chunkCV(data, i) })
		return
	}
	batches := n / 8
	parallelChunks(batches, func(b int) {
		compress8(data, b*8, cvs)
	})
	for i := batches * 8; i < n; i++ {
		cvs[i] = chunkCV(data, i)
	}
}

// bcast returns a vector with x in every lane.
func bcast(x uint32) archsimd.Uint32x8 {
	a := [8]uint32{x, x, x, x, x, x, x, x}
	return archsimd.LoadUint32x8Slice(a[:])
}

// rotr rotates every 32-bit lane right by k (0 < k < 32) using two shifts and
// an or. We deliberately avoid archsimd's RotateAllRight, which the compiler
// lowers to the AVX-512 VPROLD instruction; shifts+or stay within AVX2, so the
// kernel runs on any AVX2 CPU (matching the archsimd.X86.AVX2() gate).
func rotr(x archsimd.Uint32x8, k uint64) archsimd.Uint32x8 {
	return x.ShiftAllRight(k).Or(x.ShiftAllLeft(32 - k))
}

// gVec is the BLAKE3 quarter-round on 8 lanes at once (rotations are right by
// 16/12/8/7, matching the scalar RotateLeft32(x, -k)).
func gVec(va, vb, vc, vd, mx, my archsimd.Uint32x8) (archsimd.Uint32x8, archsimd.Uint32x8, archsimd.Uint32x8, archsimd.Uint32x8) {
	va = va.Add(vb).Add(mx)
	vd = rotr(vd.Xor(va), 16)
	vc = vc.Add(vd)
	vb = rotr(vb.Xor(vc), 12)
	va = va.Add(vb).Add(my)
	vd = rotr(vd.Xor(va), 8)
	vc = vc.Add(vd)
	vb = rotr(vb.Xor(vc), 7)
	return va, vb, vc, vd
}

// compress8 hashes the 8 full 1024-byte chunks at chunk indices base..base+7
// (lane j = chunk base+j) and writes their chaining values into cvs.
func compress8(data []byte, base int, cvs [][8]uint32) {
	// Per-lane chunk counters (chunk index), split into low/high 32 bits.
	var ctrLo, ctrHi [8]uint32
	for j := 0; j < 8; j++ {
		c := uint64(base + j)
		ctrLo[j] = uint32(c)
		ctrHi[j] = uint32(c >> 32)
	}
	vCtrLo := archsimd.LoadUint32x8Slice(ctrLo[:])
	vCtrHi := archsimd.LoadUint32x8Slice(ctrHi[:])
	vBlockLen := bcast(blockLen)

	// Chaining value, IV broadcast across lanes.
	var cv [8]archsimd.Uint32x8
	var ivv [4]archsimd.Uint32x8
	for i := 0; i < 8; i++ {
		cv[i] = bcast(iv[i])
	}
	for i := 0; i < 4; i++ {
		ivv[i] = bcast(iv[i])
	}

	// A full chunk is 16 blocks of 64 bytes; block 0 carries CHUNK_START and
	// block 15 carries CHUNK_END, mirroring chunkState.update + output().
	for b := 0; b < 16; b++ {
		// Transpose the 8 chunks' block words: m[i] lane j = word i of chunk j.
		var scratch [16][8]uint32
		for j := 0; j < 8; j++ {
			blk := data[(base+j)*chunkLen+b*blockLen:]
			for i := 0; i < 16; i++ {
				scratch[i][j] = binary.LittleEndian.Uint32(blk[i*4:])
			}
		}
		var m [16]archsimd.Uint32x8
		for i := 0; i < 16; i++ {
			m[i] = archsimd.LoadUint32x8Slice(scratch[i][:])
		}

		flags := uint32(0)
		if b == 0 {
			flags = flagChunkStart
		}
		if b == 15 {
			flags |= flagChunkEnd
		}

		var v [16]archsimd.Uint32x8
		v[0], v[1], v[2], v[3] = cv[0], cv[1], cv[2], cv[3]
		v[4], v[5], v[6], v[7] = cv[4], cv[5], cv[6], cv[7]
		v[8], v[9], v[10], v[11] = ivv[0], ivv[1], ivv[2], ivv[3]
		v[12], v[13], v[14], v[15] = vCtrLo, vCtrHi, vBlockLen, bcast(flags)

		for r := 0; r < 7; r++ {
			sc := &msgSchedule[r]
			v[0], v[4], v[8], v[12] = gVec(v[0], v[4], v[8], v[12], m[sc[0]], m[sc[1]])
			v[1], v[5], v[9], v[13] = gVec(v[1], v[5], v[9], v[13], m[sc[2]], m[sc[3]])
			v[2], v[6], v[10], v[14] = gVec(v[2], v[6], v[10], v[14], m[sc[4]], m[sc[5]])
			v[3], v[7], v[11], v[15] = gVec(v[3], v[7], v[11], v[15], m[sc[6]], m[sc[7]])
			v[0], v[5], v[10], v[15] = gVec(v[0], v[5], v[10], v[15], m[sc[8]], m[sc[9]])
			v[1], v[6], v[11], v[12] = gVec(v[1], v[6], v[11], v[12], m[sc[10]], m[sc[11]])
			v[2], v[7], v[8], v[13] = gVec(v[2], v[7], v[8], v[13], m[sc[12]], m[sc[13]])
			v[3], v[4], v[9], v[14] = gVec(v[3], v[4], v[9], v[14], m[sc[14]], m[sc[15]])
		}
		for i := 0; i < 8; i++ {
			cv[i] = v[i].Xor(v[i+8])
		}
	}

	// Scatter lane j of each CV word back to chunk base+j.
	var lane [8]uint32
	for i := 0; i < 8; i++ {
		cv[i].StoreSlice(lane[:])
		for j := 0; j < 8; j++ {
			cvs[base+j][i] = lane[j]
		}
	}
}
