//go:build ignore

// Command gen produces mix4_s390x.s with go-asmgen: the BLAKE3 7-round 4-lane
// mixing kernel on the z/Architecture vector facility (4 chunks per call, one
// per 32-bit lane). Memory-resident state like the SSE/LSX/RVV/VSX kernels: each
// quarter-round loads its four state words (each a 128-bit vector of 4 lanes),
// mixes, and stores them back.
//
// Big-endian, but the digest is still bit-exact, and mix4 itself needs NO
// byte-reversal. Reason: the state/message planes are Go `[16][4]uint32`, so Go
// writes and reads each word as a native big-endian uint32; VL loads those 16
// bytes into the vector's four 32-bit lanes with the same big-endian
// interpretation, VAF/VX/VERLLF operate per word on the correct numeric values,
// and VST writes them back identically. No lane is ever moved across another, so
// the host endianness is invisible to the per-lane arithmetic.
//
// The endianness that DOES matter is the little-endian message-word load from
// the raw BLAKE3 block bytes — but that is a Go-side concern handled in
// compress4ASM (binary.LittleEndian.Uint32, matching the scalar path), not here.
//
// Rotate: VERLLF is rotate-left-logical word by an immediate. BLAKE3's rotr(n)
// is rotl(32-n), so the rotates are VERLLF $16/$20/$24/$25.
package main

import (
	"fmt"
	"os"

	"github.com/go-asmgen/asmgen/abi"
	"github.com/go-asmgen/asmgen/emit"
	"github.com/go-asmgen/asmgen/s390x"
)

var msgPermutation = [16]int{2, 6, 3, 10, 7, 0, 4, 13, 1, 11, 12, 5, 9, 14, 15, 8}

func schedule() [7][16]int {
	var s [7][16]int
	for i := 0; i < 16; i++ {
		s[0][i] = i
	}
	for r := 1; r < 7; r++ {
		for i := 0; i < 16; i++ {
			s[r][i] = s[r-1][msgPermutation[i]]
		}
	}
	return s
}

// loadWord emits: V<dst> = mem[base + off] (one 128-bit vector = 4 lanes).
// s390x VL takes a displacement+base address, so the offset is encoded inline.
func loadWord(b *s390x.Builder, base string, off, dst int) {
	b.Raw("VL %d(%s), V%d", off, base, dst)
}

func storeWord(b *s390x.Builder, base string, off, src int) {
	b.Raw("VST V%d, %d(%s)", src, off, base)
}

// gVec emits one quarter-round. R1 = state ptr, R2 = m ptr. Working words in
// V0(a) V1(b) V2(c) V3(d); V4 message. Plan 9 reversed-operand form:
// `VAF src, src, dst` => dst = srcA + srcB; `VX src, src, dst` => dst = a ^ b;
// `VERLLF $n, src, dst` => dst = rotl(src, n).
func gVec(b *s390x.Builder, a, bb, c, d, scx, scy int) {
	loadWord(b, "R1", a*16, 0)
	loadWord(b, "R1", bb*16, 1)
	loadWord(b, "R1", c*16, 2)
	loadWord(b, "R1", d*16, 3)
	b.Raw("VAF V1, V0, V0")      // a += b
	loadWord(b, "R2", scx*16, 4) // mx
	b.Raw("VAF V4, V0, V0")      // a += mx
	b.Raw("VX V0, V3, V3")       // d ^= a
	b.Raw("VERLLF $16, V3, V3")  // d = rotr(d,16) = rotl(d,16)
	b.Raw("VAF V3, V2, V2")      // c += d
	b.Raw("VX V2, V1, V1")       // b ^= c
	b.Raw("VERLLF $20, V1, V1")  // b = rotr(b,12) = rotl(b,20)
	b.Raw("VAF V1, V0, V0")      // a += b
	loadWord(b, "R2", scy*16, 4) // my
	b.Raw("VAF V4, V0, V0")      // a += my
	b.Raw("VX V0, V3, V3")       // d ^= a
	b.Raw("VERLLF $24, V3, V3")  // d = rotr(d,8) = rotl(d,24)
	b.Raw("VAF V3, V2, V2")      // c += d
	b.Raw("VX V2, V1, V1")       // b ^= c
	b.Raw("VERLLF $25, V1, V1")  // b = rotr(b,7) = rotl(b,25)
	storeWord(b, "R1", a*16, 0)
	storeWord(b, "R1", bb*16, 1)
	storeWord(b, "R1", c*16, 2)
	storeWord(b, "R1", d*16, 3)
}

func main() {
	sig := abi.LayoutArgs(
		[]abi.Arg{abi.Scalar("state", abi.Ptr), abi.Scalar("m", abi.Ptr)},
		nil,
	)
	b := s390x.NewFunc("mix4", sig, 0)
	b.LoadArg("state", "R1").LoadArg("m", "R2")

	cols := [8][4]int{
		{0, 4, 8, 12}, {1, 5, 9, 13}, {2, 6, 10, 14}, {3, 7, 11, 15},
		{0, 5, 10, 15}, {1, 6, 11, 12}, {2, 7, 8, 13}, {3, 4, 9, 14},
	}
	sched := schedule()
	for r := 0; r < 7; r++ {
		for gi := 0; gi < 8; gi++ {
			c := cols[gi]
			gVec(b, c[0], c[1], c[2], c[3], sched[r][2*gi], sched[r][2*gi+1])
		}
	}
	b.Ret()

	f := emit.NewFile("s390x && !goexperiment.simd")
	f.Add(b.Func())
	if err := os.WriteFile("mix4_s390x.s", []byte(f.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote mix4_s390x.s")
}
