//go:build goexperiment.simd && arm64

// EXPERIMENTAL, cgo-free, assembly-free SIMD acceleration of BLAKE3's per-chunk
// compression for arm64 (NEON), using Go's experimental `simd/archsimd`
// intrinsics. Unlike a function-call SIMD library, these are compiler
// intrinsics — inlined and register-allocated — so they suit BLAKE3's
// fine-grained kernel. Requires Go 1.27+ (arm64 archsimd landed on dev.simd)
// and GOEXPERIMENT=simd; every other build is byte-for-byte unaffected.
//
// NEON registers are 128-bit, so we hash 4 full 1024-byte chunks at once — one
// chunk per uint32 lane (Uint32x4). Mixing is pure elementwise Add/Xor plus a
// shift-or rotate (NEON has no vector rotate). Results are bit-identical to the
// scalar path, verified against the official BLAKE3 vectors under this build.

package blake3

import (
	"runtime"
	"simd/archsimd"
	"unsafe"
)

const simdLanes = 4 // NEON: 128-bit / 32-bit lanes

func fillChunkCVs(data []byte, cvs [][8]uint32) {
	n := len(cvs)
	batches := n / simdLanes
	// SIMD only pays off once there are enough batches to fill the CPUs and
	// amortize the per-batch transpose; below that the scalar per-chunk path
	// (more, lighter tasks) is faster. Measured crossover on a 16-core M4 Max:
	// scalar wins at 64 KiB (15 batches), NEON wins from 80 KiB (19 batches) —
	// i.e. right at batches ≈ NumCPU. Gating here keeps us on the faster side
	// at every size (no SIMD slowdown for small inputs).
	if batches < runtime.NumCPU() {
		fillChunkCVsScalar(data, cvs)
		return
	}
	parallelChunks(batches, func(b int) {
		compress4(data, b*simdLanes, cvs)
	})
	for i := batches * simdLanes; i < n; i++ {
		cvs[i] = chunkCV(data, i)
	}
}

// rotr rotates each 32-bit lane right by k via shift+or (NEON has no rotate).
func rotr(x archsimd.Uint32x4, k uint64) archsimd.Uint32x4 {
	return x.ShiftAllRight(k).Or(x.ShiftAllLeft(32 - k))
}

// transpose4 returns the columns of the 4×4 matrix whose rows are r0..r3:
// result k = {r0[k], r1[k], r2[k], r3[k]}. Two 32-bit transposes (TRN1/TRN2)
// then two 64-bit interleaves (ZIP1/ZIP2) — the standard NEON 4×4 transpose,
// expressed with archsimd intrinsics (no scalar gather).
func transpose4(r0, r1, r2, r3 archsimd.Uint32x4) (archsimd.Uint32x4, archsimd.Uint32x4, archsimd.Uint32x4, archsimd.Uint32x4) {
	t0 := r0.TransposeEven(r1)
	t1 := r0.TransposeOdd(r1)
	t2 := r2.TransposeEven(r3)
	t3 := r2.TransposeOdd(r3)
	a0 := t0.AsUint64x2()
	a1 := t1.AsUint64x2()
	a2 := t2.AsUint64x2()
	a3 := t3.AsUint64x2()
	return a0.InterleaveLo(a2).AsUint32x4(), // col 0
		a1.InterleaveLo(a3).AsUint32x4(), // col 1
		a0.InterleaveHi(a2).AsUint32x4(), // col 2
		a1.InterleaveHi(a3).AsUint32x4() // col 3
}

func gVec(va, vb, vc, vd, mx, my archsimd.Uint32x4) (archsimd.Uint32x4, archsimd.Uint32x4, archsimd.Uint32x4, archsimd.Uint32x4) {
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

// compress4 hashes the 4 full 1024-byte chunks at indices base..base+3
// (lane j = chunk base+j) and writes their chaining values into cvs.
func compress4(data []byte, base int, cvs [][8]uint32) {
	var ctrLo, ctrHi [simdLanes]uint32
	for j := 0; j < simdLanes; j++ {
		c := uint64(base + j)
		ctrLo[j] = uint32(c)
		ctrHi[j] = uint32(c >> 32)
	}
	vCtrLo := archsimd.LoadUint32x4(ctrLo[:])
	vCtrHi := archsimd.LoadUint32x4(ctrHi[:])
	vBlockLen := archsimd.BroadcastUint32x4(blockLen)

	var cv [8]archsimd.Uint32x4
	var ivv [4]archsimd.Uint32x4
	for i := 0; i < 8; i++ {
		cv[i] = archsimd.BroadcastUint32x4(iv[i])
	}
	for i := 0; i < 4; i++ {
		ivv[i] = archsimd.BroadcastUint32x4(iv[i])
	}

	for b := 0; b < 16; b++ {
		// Load each chunk's 16 message words directly as vectors — on a
		// little-endian arch the chunk bytes are the LE words — and transpose
		// 4 chunks at a time so m[i] lane j = word i of chunk j. No scalar gather.
		w0 := unsafe.Slice((*uint32)(unsafe.Pointer(&data[(base+0)*chunkLen+b*blockLen])), 16)
		w1 := unsafe.Slice((*uint32)(unsafe.Pointer(&data[(base+1)*chunkLen+b*blockLen])), 16)
		w2 := unsafe.Slice((*uint32)(unsafe.Pointer(&data[(base+2)*chunkLen+b*blockLen])), 16)
		w3 := unsafe.Slice((*uint32)(unsafe.Pointer(&data[(base+3)*chunkLen+b*blockLen])), 16)
		var m [16]archsimd.Uint32x4
		for g := 0; g < 16; g += simdLanes {
			m[g], m[g+1], m[g+2], m[g+3] = transpose4(
				archsimd.LoadUint32x4(w0[g:]),
				archsimd.LoadUint32x4(w1[g:]),
				archsimd.LoadUint32x4(w2[g:]),
				archsimd.LoadUint32x4(w3[g:]),
			)
		}

		flags := uint32(0)
		if b == 0 {
			flags = flagChunkStart
		}
		if b == 15 {
			flags |= flagChunkEnd
		}

		var v [16]archsimd.Uint32x4
		v[0], v[1], v[2], v[3] = cv[0], cv[1], cv[2], cv[3]
		v[4], v[5], v[6], v[7] = cv[4], cv[5], cv[6], cv[7]
		v[8], v[9], v[10], v[11] = ivv[0], ivv[1], ivv[2], ivv[3]
		v[12], v[13], v[14], v[15] = vCtrLo, vCtrHi, vBlockLen, archsimd.BroadcastUint32x4(flags)

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

	var lane [simdLanes]uint32
	for i := 0; i < 8; i++ {
		cv[i].Store(lane[:])
		for j := 0; j < simdLanes; j++ {
			cvs[base+j][i] = lane[j]
		}
	}
}
