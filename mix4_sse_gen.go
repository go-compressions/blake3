//go:build ignore

// Command gen produces mix4_amd64.s with go-asmgen: the BLAKE3 7-round, 4-lane
// mixing kernel in SSE2 (4 chunks in parallel, one per 32-bit lane). Companion
// to mix4_neon_gen.go (arm64). x86 has only 16 XMM registers (vs arm64's 32), too
// few to hold all 16 state words plus a rotate temp, so the state lives in memory
// (*state) and each quarter-round loads its four words, mixes, and stores them
// back — message words come straight from memory operands. Plenty of XMM are then
// free for scratch. State stays hot in L1, so the four-wide win still lands.
package main

import (
	"fmt"
	"os"

	"github.com/go-asmgen/asmgen/abi"
	"github.com/go-asmgen/asmgen/amd64"
	"github.com/go-asmgen/asmgen/emit"
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

// rotr emits a 32-bit-lane rotate-right of Xreg by n, using X4 as scratch.
// n==16 is a free 16-bit word swap; the others are shift-or.
func rotr(b *amd64.Builder, reg, n int) {
	if n == 16 {
		b.Raw("PSHUFLW $0xB1, X%d, X%d", reg, reg)
		b.Raw("PSHUFHW $0xB1, X%d, X%d", reg, reg)
		return
	}
	b.Raw("MOVO X%d, X4", reg)
	b.Raw("PSRLL $%d, X%d", n, reg)
	b.Raw("PSLLL $%d, X4", 32-n)
	b.Raw("POR X4, X%d", reg)
}

// gVec emits one quarter-round on state words a,b,c,d (DI = state ptr, SI = m
// ptr). Working copies live in X0(a) X1(b) X2(c) X3(d); X4 is rotate scratch.
func gVec(b *amd64.Builder, a, bb, c, d, scx, scy int) {
	b.Raw("MOVOU %d(DI), X0", a*16)
	b.Raw("MOVOU %d(DI), X1", bb*16)
	b.Raw("MOVOU %d(DI), X2", c*16)
	b.Raw("MOVOU %d(DI), X3", d*16)
	b.Raw("PADDL X1, X0")             // a += b
	b.Raw("PADDL %d(SI), X0", scx*16) // a += mx
	b.Raw("PXOR X0, X3")              // d ^= a
	rotr(b, 3, 16)
	b.Raw("PADDL X3, X2") // c += d
	b.Raw("PXOR X2, X1")  // b ^= c
	rotr(b, 1, 12)
	b.Raw("PADDL X1, X0")             // a += b
	b.Raw("PADDL %d(SI), X0", scy*16) // a += my
	b.Raw("PXOR X0, X3")              // d ^= a
	rotr(b, 3, 8)
	b.Raw("PADDL X3, X2") // c += d
	b.Raw("PXOR X2, X1")  // b ^= c
	rotr(b, 1, 7)
	b.Raw("MOVOU X0, %d(DI)", a*16)
	b.Raw("MOVOU X1, %d(DI)", bb*16)
	b.Raw("MOVOU X2, %d(DI)", c*16)
	b.Raw("MOVOU X3, %d(DI)", d*16)
}

func main() {
	sig := abi.LayoutArgs(
		[]abi.Arg{abi.Scalar("state", abi.Ptr), abi.Scalar("m", abi.Ptr)},
		nil,
	)
	b := amd64.NewFunc("mix4", sig, 0)
	b.LoadArg("state", "DI").LoadArg("m", "SI")

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

	f := emit.NewFile("amd64 && !goexperiment.simd")
	f.Add(b.Func())
	if err := os.WriteFile("mix4_amd64.s", []byte(f.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote mix4_amd64.s")
}
