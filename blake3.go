// Package blake3 is a pure-Go, cgo-free implementation of the BLAKE3
// cryptographic hash function (default, unkeyed mode), verified against the
// official BLAKE3 test vectors. It offers fixed-size helpers (Sum256, Sum512),
// a streaming hash.Hash-style Hasher, and arbitrary-length extendable output
// (Hasher.Digest).
package blake3

import (
	"encoding/binary"
	"math/bits"
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

// msgPermutation is the per-round message word permutation.
var msgPermutation = [16]int{2, 6, 3, 10, 7, 0, 4, 13, 1, 11, 12, 5, 9, 14, 15, 8}

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

func round(s, m *[16]uint32) {
	// Columns.
	g(s, 0, 4, 8, 12, m[0], m[1])
	g(s, 1, 5, 9, 13, m[2], m[3])
	g(s, 2, 6, 10, 14, m[4], m[5])
	g(s, 3, 7, 11, 15, m[6], m[7])
	// Diagonals.
	g(s, 0, 5, 10, 15, m[8], m[9])
	g(s, 1, 6, 11, 12, m[10], m[11])
	g(s, 2, 7, 8, 13, m[12], m[13])
	g(s, 3, 4, 9, 14, m[14], m[15])
}

func permute(m *[16]uint32) {
	var p [16]uint32
	for i := 0; i < 16; i++ {
		p[i] = m[msgPermutation[i]]
	}
	*m = p
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
		round(&state, &m)
		if r < 6 {
			permute(&m)
		}
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

// Sum256 returns the 32-byte BLAKE3 digest of data.
func Sum256(data []byte) [32]byte {
	h := New()
	h.Write(data)
	var out [32]byte
	h.finalize(out[:])
	return out
}

// Sum512 returns the first 64 bytes of BLAKE3 extendable output for data.
func Sum512(data []byte) [64]byte {
	h := New()
	h.Write(data)
	var out [64]byte
	h.finalize(out[:])
	return out
}
