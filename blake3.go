// Package blake3 is a pure-Go, cgo-free implementation of the BLAKE3
// cryptographic hash function (default, unkeyed mode), verified against the
// official BLAKE3 test vectors. It offers fixed-size helpers (Sum256, Sum512),
// a streaming hash.Hash-style Hasher, and arbitrary-length extendable output
// (Hasher.Digest).
package blake3

import (
	"encoding/binary"
	"math/bits"
	"runtime"
	"sync"
	"sync/atomic"
)

const (
	blockLen = 64   // bytes per compression input block
	chunkLen = 1024 // bytes per chunk (16 blocks)

	flagChunkStart uint32 = 1 << 0
	flagChunkEnd   uint32 = 1 << 1
	flagParent     uint32 = 1 << 2
	flagRoot       uint32 = 1 << 3
)

// iv is the BLAKE3 initial chaining value (the SHA-256 IV).
var iv = [8]uint32{
	0x6A09E667, 0xBB67AE85, 0x3C6EF372, 0xA54FF53A,
	0x510E527F, 0x9B05688C, 0x1F83D9AB, 0x5BE0CD19,
}

// msgPermutation is the per-round message word permutation (RFC/spec).
var msgPermutation = [16]int{2, 6, 3, 10, 7, 0, 4, 13, 1, 11, 12, 5, 9, 14, 15, 8}

// msgSchedule precomputes, for each of the 7 rounds, the message-word index used
// at each of the 16 mixing slots. It folds the per-round permutation into a
// lookup so compress can index the original message directly instead of
// physically permuting a 16-word array six times per block. Output is identical
// to applying msgPermutation between rounds.
var msgSchedule = func() [7][16]uint8 {
	var s [7][16]uint8
	for i := 0; i < 16; i++ {
		s[0][i] = uint8(i)
	}
	for r := 1; r < 7; r++ {
		for i := 0; i < 16; i++ {
			s[r][i] = s[r-1][msgPermutation[i]]
		}
	}
	return s
}()

// g is the BLAKE3 quarter-round mixing function.
func g(s *[16]uint32, a, b, c, d int, mx, my uint32) {
	s[a] = s[a] + s[b] + mx
	s[d] = bits.RotateLeft32(s[d]^s[a], -16)
	s[c] = s[c] + s[d]
	s[b] = bits.RotateLeft32(s[b]^s[c], -12)
	s[a] = s[a] + s[b] + my
	s[d] = bits.RotateLeft32(s[d]^s[a], -8)
	s[c] = s[c] + s[d]
	s[b] = bits.RotateLeft32(s[b]^s[c], -7)
}

func round(s, m *[16]uint32, r int) {
	sc := &msgSchedule[r]
	// Columns.
	g(s, 0, 4, 8, 12, m[sc[0]], m[sc[1]])
	g(s, 1, 5, 9, 13, m[sc[2]], m[sc[3]])
	g(s, 2, 6, 10, 14, m[sc[4]], m[sc[5]])
	g(s, 3, 7, 11, 15, m[sc[6]], m[sc[7]])
	// Diagonals.
	g(s, 0, 5, 10, 15, m[sc[8]], m[sc[9]])
	g(s, 1, 6, 11, 12, m[sc[10]], m[sc[11]])
	g(s, 2, 7, 8, 13, m[sc[12]], m[sc[13]])
	g(s, 3, 4, 9, 14, m[sc[14]], m[sc[15]])
}

// compress runs the 7-round BLAKE3 compression and returns the 16-word state.
// The first 8 words are the chaining value; all 16 are used for output.
func compress(cv *[8]uint32, block *[16]uint32, counter uint64, blkLen, flags uint32) [16]uint32 {
	state := [16]uint32{
		cv[0], cv[1], cv[2], cv[3], cv[4], cv[5], cv[6], cv[7],
		iv[0], iv[1], iv[2], iv[3],
		uint32(counter), uint32(counter >> 32), blkLen, flags,
	}
	m := *block
	for r := 0; r < 7; r++ {
		round(&state, &m, r)
	}
	for i := 0; i < 8; i++ {
		state[i] ^= state[i+8]
		state[i+8] ^= cv[i]
	}
	return state
}

func first8(s [16]uint32) [8]uint32 {
	var cv [8]uint32
	copy(cv[:], s[:8])
	return cv
}

// wordsFromLE fills 16 little-endian words from b (zero-padded if b < 64 bytes).
func wordsFromLE(b []byte) [16]uint32 {
	var w [16]uint32
	var buf [blockLen]byte
	copy(buf[:], b)
	for i := 0; i < 16; i++ {
		w[i] = binary.LittleEndian.Uint32(buf[i*4:])
	}
	return w
}

// output is a node (chunk or parent) ready to be finalized into a chaining
// value or extendable root output.
type output struct {
	inputCV  [8]uint32
	block    [16]uint32
	counter  uint64
	blockLen uint32
	flags    uint32
}

func (o output) chainingValue() [8]uint32 {
	return first8(compress(&o.inputCV, &o.block, o.counter, o.blockLen, o.flags))
}

// rootOutput fills out with the extendable root output (XOF).
func (o *output) rootOutput(out []byte) {
	counter := uint64(0)
	for len(out) > 0 {
		words := compress(&o.inputCV, &o.block, counter, o.blockLen, o.flags|flagRoot)
		var buf [blockLen]byte
		for i, w := range words {
			binary.LittleEndian.PutUint32(buf[i*4:], w)
		}
		n := copy(out, buf[:])
		out = out[n:]
		counter++
	}
}

func parentOutput(left, right [8]uint32) output {
	var block [16]uint32
	copy(block[:8], left[:])
	copy(block[8:], right[:])
	return output{inputCV: iv, block: block, blockLen: blockLen, flags: flagParent}
}

// chunkState accumulates one chunk (up to 1024 bytes) of input.
type chunkState struct {
	cv               [8]uint32
	chunkCounter     uint64
	block            [blockLen]byte
	blockLen         int
	blocksCompressed int
}

func newChunkState(counter uint64) chunkState {
	return chunkState{cv: iv, chunkCounter: counter}
}

func (c *chunkState) len() int { return blockLen*c.blocksCompressed + c.blockLen }

func (c *chunkState) startFlag() uint32 {
	if c.blocksCompressed == 0 {
		return flagChunkStart
	}
	return 0
}

func (c *chunkState) update(input []byte) {
	for len(input) > 0 {
		if c.blockLen == blockLen {
			w := wordsFromLE(c.block[:])
			c.cv = first8(compress(&c.cv, &w, c.chunkCounter, blockLen, c.startFlag()))
			c.blocksCompressed++
			c.block = [blockLen]byte{}
			c.blockLen = 0
		}
		n := copy(c.block[c.blockLen:], input)
		c.blockLen += n
		input = input[n:]
	}
}

func (c *chunkState) output() output {
	return output{
		inputCV:  c.cv,
		block:    wordsFromLE(c.block[:]),
		counter:  c.chunkCounter,
		blockLen: uint32(c.blockLen),
		flags:    c.startFlag() | flagChunkEnd,
	}
}

// Hasher is an incremental BLAKE3 hasher. It is not safe for concurrent use.
// The zero value is not usable; obtain one from New.
type Hasher struct {
	chunk      chunkState
	cvStack    [54][8]uint32 // one slot per tree level (covers any practical input)
	cvStackLen int
}

// New returns a new streaming BLAKE3 hasher in the default (unkeyed) mode.
func New() *Hasher {
	return &Hasher{chunk: newChunkState(0)}
}

// Size returns the default digest size in bytes (32).
func (h *Hasher) Size() int { return 32 }

// BlockSize returns the hash block size in bytes (64).
func (h *Hasher) BlockSize() int { return blockLen }

// Reset restores the hasher to its initial state.
func (h *Hasher) Reset() {
	h.chunk = newChunkState(0)
	h.cvStackLen = 0
}

func (h *Hasher) pushCV(cv [8]uint32) {
	h.cvStack[h.cvStackLen] = cv
	h.cvStackLen++
}

func (h *Hasher) popCV() [8]uint32 {
	h.cvStackLen--
	return h.cvStack[h.cvStackLen]
}

// addChunkCV folds a finished chunk's chaining value into the tree, merging
// completed subtrees (mirroring the reference add_chunk_chaining_value).
func (h *Hasher) addChunkCV(cv [8]uint32, totalChunks uint64) {
	for totalChunks&1 == 0 {
		cv = parentOutput(h.popCV(), cv).chainingValue()
		totalChunks >>= 1
	}
	h.pushCV(cv)
}

// Write absorbs input. It never returns an error.
func (h *Hasher) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		if h.chunk.len() == chunkLen {
			cv := h.chunk.output().chainingValue()
			total := h.chunk.chunkCounter + 1
			h.addChunkCV(cv, total)
			h.chunk = newChunkState(total)
		}
		want := chunkLen - h.chunk.len()
		if want > len(p) {
			want = len(p)
		}
		h.chunk.update(p[:want])
		p = p[want:]
	}
	return n, nil
}

// finalize computes the extendable output into out without mutating the hasher.
func (h *Hasher) finalize(out []byte) {
	o := h.chunk.output()
	for i := h.cvStackLen - 1; i >= 0; i-- {
		o = parentOutput(h.cvStack[i], o.chainingValue())
	}
	o.rootOutput(out)
}

// Digest writes len(out) bytes of extendable (XOF) output. Calling it does not
// change the hasher state, so more data may still be written afterwards.
func (h *Hasher) Digest(out []byte) {
	h.finalize(out)
}

// Sum appends the 32-byte BLAKE3 digest of the data written so far to b.
func (h *Hasher) Sum(b []byte) []byte {
	var d [32]byte
	h.finalize(d[:])
	return append(b, d[:]...)
}

// parallelMinChunks is the chunk-count below which the per-chunk work is too
// small to outweigh goroutine scheduling, so we run inline.
const parallelMinChunks = 16

// parallelChunks runs fn(0..n-1) across CPU cores; fn must only touch state
// private to its index (here, a distinct slice element). n is always >= 1.
func parallelChunks(n int, fn func(int)) {
	if n < parallelMinChunks {
		for i := 0; i < n; i++ {
			fn(i)
		}
		return
	}
	workers := runtime.NumCPU()
	var wg sync.WaitGroup
	var next int64 = -1
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for {
				i := int(atomic.AddInt64(&next, 1))
				if i >= n {
					return
				}
				fn(i)
			}
		}()
	}
	wg.Wait()
}

// chunkCV computes the chaining value of the full 1024-byte chunk at chunk
// index i (data[i*chunkLen:(i+1)*chunkLen]). It is the scalar per-chunk kernel
// shared by the generic fillChunkCVs and the SIMD path's remainder/fallback.
func chunkCV(data []byte, i int) [8]uint32 {
	cs := newChunkState(uint64(i))
	cs.update(data[i*chunkLen : (i+1)*chunkLen])
	return cs.output().chainingValue()
}

// fillChunkCVs computes the chaining value of each full 1024-byte chunk
// data[i*chunkLen:(i+1)*chunkLen] into cvs[i]. Its implementation is selected at
// build time: a pure-Go version (blake3_generic.go) and an experimental amd64
// SIMD version under GOEXPERIMENT=simd (blake3_simd_amd64.go) that produces
// bit-identical results.

// hashAll computes the root output node for the whole input in one shot. For
// inputs larger than one chunk the independent per-chunk chaining values are
// computed in parallel across CPU cores (BLAKE3's tree makes them independent);
// the cheap O(chunks) tree merge stays sequential. The result is byte-identical
// to the incremental Hasher.
func hashAll(data []byte) output {
	if len(data) <= chunkLen {
		cs := newChunkState(0)
		cs.update(data)
		return cs.output()
	}
	nChunks := (len(data) + chunkLen - 1) / chunkLen
	cvs := make([][8]uint32, nChunks-1)
	fillChunkCVs(data, cvs)
	var stack Hasher
	for i, cv := range cvs {
		stack.addChunkCV(cv, uint64(i)+1)
	}
	last := newChunkState(uint64(nChunks - 1))
	last.update(data[(nChunks-1)*chunkLen:])
	o := last.output()
	for i := stack.cvStackLen - 1; i >= 0; i-- {
		o = parentOutput(stack.cvStack[i], o.chainingValue())
	}
	return o
}

// Sum256 returns the 32-byte BLAKE3 digest of data.
func Sum256(data []byte) [32]byte {
	o := hashAll(data)
	var out [32]byte
	o.rootOutput(out[:])
	return out
}

// Sum512 returns the first 64 bytes of BLAKE3 extendable output for data.
func Sum512(data []byte) [64]byte {
	o := hashAll(data)
	var out [64]byte
	o.rootOutput(out[:])
	return out
}
