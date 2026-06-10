//go:build ignore

// Command gen produces mix4_arm64.s with go-asmgen: a stable-Go NEON kernel for
// BLAKE3's 7-round mixing across 4 lanes (4 chunks hashed in parallel, one chunk
// per 32-bit lane of a Uint32x4). The existing blake3_simd_arm64.go does the same
// thing with Go 1.27+ `archsimd` intrinsics behind GOEXPERIMENT=simd; this gives
// the same SIMD on plain `go build` today.
//
// Register plan (arm64 has 32 V registers):
//
//	V0..V15  the 16-word state, kept live across all 7 rounds
//	V16,V17  the two message words of the current quarter-round (mx, my)
//	V18      scratch for the rotate
//
// State and message are [16][4]uint32 in memory; each word is a 128-bit Q load
// (FMOVQ) at a fixed immediate offset. Mixing is Add/Xor + shift-or rotate only
// (no multiply), so nothing here depends on the missing NEON integer multiply.
package main

import (
	"fmt"
	"os"

	"github.com/go-asmgen/asmgen/abi"
	"github.com/go-asmgen/asmgen/arm64"
	"github.com/go-asmgen/asmgen/emit"
)

// msgPermutation / schedule mirror blake3.go so the generator can bake each
// round's message-word indices straight into the unrolled asm.
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

// rotr emits a 32-bit-lane rotate-right of V<reg> by n, using V18 as scratch.
func rotr(b *arm64.Builder, reg, n int) {
	b.Raw("VUSHR $%d, V%d.S4, V18.S4", n, reg)
	b.Raw("VSHL $%d, V%d.S4, V%d.S4", 32-n, reg, reg)
	b.Raw("VORR V18.B16, V%d.B16, V%d.B16", reg, reg)
}

// gVec emits one BLAKE3 quarter-round on state words a,b,c,d with message words
// at schedule slots scx, scy (loaded from the message pointer in R1).
func gVec(b *arm64.Builder, a, bb, c, d, scx, scy int) {
	b.Raw("FMOVQ %d(R1), F16", scx*16)               // mx
	b.Raw("FMOVQ %d(R1), F17", scy*16)               // my
	b.Raw("VADD V%d.S4, V%d.S4, V%d.S4", bb, a, a)   // a += b
	b.Raw("VADD V16.S4, V%d.S4, V%d.S4", a, a)       // a += mx
	b.Raw("VEOR V%d.B16, V%d.B16, V%d.B16", a, d, d) // d ^= a
	rotr(b, d, 16)
	b.Raw("VADD V%d.S4, V%d.S4, V%d.S4", d, c, c)      // c += d
	b.Raw("VEOR V%d.B16, V%d.B16, V%d.B16", c, bb, bb) // b ^= c
	rotr(b, bb, 12)
	b.Raw("VADD V%d.S4, V%d.S4, V%d.S4", bb, a, a)   // a += b
	b.Raw("VADD V17.S4, V%d.S4, V%d.S4", a, a)       // a += my
	b.Raw("VEOR V%d.B16, V%d.B16, V%d.B16", a, d, d) // d ^= a
	rotr(b, d, 8)
	b.Raw("VADD V%d.S4, V%d.S4, V%d.S4", d, c, c)      // c += d
	b.Raw("VEOR V%d.B16, V%d.B16, V%d.B16", c, bb, bb) // b ^= c
	rotr(b, bb, 7)
}

func main() {
	// func mix4(state *[16][4]uint32, m *[16][4]uint32)
	sig := abi.LayoutArgs(
		[]abi.Arg{abi.Scalar("state", abi.Ptr), abi.Scalar("m", abi.Ptr)},
		nil,
	)
	b := arm64.NewFunc("mix4", sig, 0)
	b.LoadArg("state", "R0").LoadArg("m", "R1")

	for i := 0; i < 16; i++ { // load state into V0..V15
		b.Raw("FMOVQ %d(R0), F%d", i*16, i)
	}

	// The 8 quarter-rounds per round: 4 columns then 4 diagonals.
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

	for i := 0; i < 16; i++ { // store state back
		b.Raw("FMOVQ F%d, %d(R0)", i, i*16)
	}
	b.Ret()

	f := emit.NewFile("arm64 && !goexperiment.simd")
	f.Add(b.Func())
	if err := os.WriteFile("mix4_arm64.s", []byte(f.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote mix4_arm64.s")
}
