//go:build ignore

// Command gen produces mix4_riscv64.s with go-asmgen: the BLAKE3 7-round 4-lane
// mixing kernel in RVV (RISC-V Vector, 4 chunks per call, one per 32-bit lane).
// VL is pinned to 4 elements of e32 (one VSETVLI up front; with VLEN>=128 the
// AVL of 4 caps it). Memory-resident state like the SSE/LSX kernels: RVV loads
// address a base register, so each word's address is computed with an ADD. Base
// RVV has no rotate, so rotates are shift-or.
package main

import (
	"fmt"
	"os"

	"github.com/go-asmgen/asmgen/abi"
	"github.com/go-asmgen/asmgen/emit"
	"github.com/go-asmgen/asmgen/riscv64"
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

// rotr emits a 32-bit-lane rotate-right of V<reg> by n. go1.27+ Zvbb variant: a
// single vror.vi replaces the base-RVV VSRLVI+VSLLVI+VORVV emulation (3 ops -> 1).
// Requires the Zvbb extension at runtime; assembles only on go1.27+.
func rotr(b *riscv64.Builder, reg, n int) {
	b.Raw("VRORVI $%d, V%d, V%d", n, reg, reg)
}

// loadWord emits: addr = base + off; V<dst> = mem[addr] (VL words).
func loadWord(b *riscv64.Builder, base string, off, dst int) {
	b.Raw("ADD $%d, %s, X7", off, base)
	b.Raw("VLE32V (X7), V%d", dst)
}

// gVec emits one quarter-round. X5 = state ptr, X6 = m ptr, X7 = addr scratch.
// Working words: V1(a) V2(b) V3(c) V4(d); V5 message; V6 rotate scratch.
func gVec(b *riscv64.Builder, a, bb, c, d, scx, scy int) {
	loadWord(b, "X5", a*16, 1)
	loadWord(b, "X5", bb*16, 2)
	loadWord(b, "X5", c*16, 3)
	loadWord(b, "X5", d*16, 4)
	b.Raw("VADDVV V2, V1, V1") // a += b
	loadWord(b, "X6", scx*16, 5)
	b.Raw("VADDVV V5, V1, V1") // a += mx
	b.Raw("VXORVV V1, V4, V4") // d ^= a
	rotr(b, 4, 16)
	b.Raw("VADDVV V4, V3, V3") // c += d
	b.Raw("VXORVV V3, V2, V2") // b ^= c
	rotr(b, 2, 12)
	b.Raw("VADDVV V2, V1, V1") // a += b
	loadWord(b, "X6", scy*16, 5)
	b.Raw("VADDVV V5, V1, V1") // a += my
	b.Raw("VXORVV V1, V4, V4") // d ^= a
	rotr(b, 4, 8)
	b.Raw("VADDVV V4, V3, V3") // c += d
	b.Raw("VXORVV V3, V2, V2") // b ^= c
	rotr(b, 2, 7)
	b.Raw("ADD $%d, X5, X7", a*16)
	b.Raw("VSE32V V1, (X7)")
	b.Raw("ADD $%d, X5, X7", bb*16)
	b.Raw("VSE32V V2, (X7)")
	b.Raw("ADD $%d, X5, X7", c*16)
	b.Raw("VSE32V V3, (X7)")
	b.Raw("ADD $%d, X5, X7", d*16)
	b.Raw("VSE32V V4, (X7)")
}

func main() {
	sig := abi.LayoutArgs(
		[]abi.Arg{abi.Scalar("state", abi.Ptr), abi.Scalar("m", abi.Ptr)},
		nil,
	)
	b := riscv64.NewFunc("mix4", sig, 0)
	b.LoadArg("state", "X5").LoadArg("m", "X6")
	b.Raw("VSETVLI $4, E32, M1, TA, MA, X8") // VL = 4 lanes of uint32

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

	f := emit.NewFile("riscv64 && !goexperiment.simd")
	f.Add(b.Func())
	if err := os.WriteFile("mix4_riscv64.s", []byte(f.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote mix4_riscv64.s")
}
