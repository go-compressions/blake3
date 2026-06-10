//go:build ignore

// Command gen produces mix4_loong64.s with go-asmgen: the BLAKE3 7-round 4-lane
// mixing kernel in LSX (loongarch 128-bit SIMD, 4 chunks per call, one per 32-bit
// lane). Memory-resident state like the SSE kernel: each quarter-round loads its
// four state words, mixes, stores back. LSX has a native word rotate (VROTRW).
package main

import (
	"fmt"
	"os"

	"github.com/go-asmgen/asmgen/abi"
	"github.com/go-asmgen/asmgen/emit"
	"github.com/go-asmgen/asmgen/loong64"
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

// gVec emits one quarter-round (R4 = state ptr, R5 = m ptr). Working words in
// V0(a) V1(b) V2(c) V3(d); V4 holds the loaded message word. LSX 3-reg form is
// `OP Vk, Vj, Vd` => Vd = Vj OP Vk; VROTRW rotates each word right by an immediate.
func gVec(b *loong64.Builder, a, bb, c, d, scx, scy int) {
	b.Raw("VMOVQ %d(R4), V0", a*16)
	b.Raw("VMOVQ %d(R4), V1", bb*16)
	b.Raw("VMOVQ %d(R4), V2", c*16)
	b.Raw("VMOVQ %d(R4), V3", d*16)
	b.Raw("VADDW V1, V0, V0")         // a += b
	b.Raw("VMOVQ %d(R5), V4", scx*16) // mx
	b.Raw("VADDW V4, V0, V0")         // a += mx
	b.Raw("VXORV V0, V3, V3")         // d ^= a
	b.Raw("VROTRW $16, V3, V3")       // d = rotr(d,16)
	b.Raw("VADDW V3, V2, V2")         // c += d
	b.Raw("VXORV V2, V1, V1")         // b ^= c
	b.Raw("VROTRW $12, V1, V1")       // b = rotr(b,12)
	b.Raw("VADDW V1, V0, V0")         // a += b
	b.Raw("VMOVQ %d(R5), V4", scy*16) // my
	b.Raw("VADDW V4, V0, V0")         // a += my
	b.Raw("VXORV V0, V3, V3")         // d ^= a
	b.Raw("VROTRW $8, V3, V3")        // d = rotr(d,8)
	b.Raw("VADDW V3, V2, V2")         // c += d
	b.Raw("VXORV V2, V1, V1")         // b ^= c
	b.Raw("VROTRW $7, V1, V1")        // b = rotr(b,7)
	b.Raw("VMOVQ V0, %d(R4)", a*16)
	b.Raw("VMOVQ V1, %d(R4)", bb*16)
	b.Raw("VMOVQ V2, %d(R4)", c*16)
	b.Raw("VMOVQ V3, %d(R4)", d*16)
}

func main() {
	sig := abi.LayoutArgs(
		[]abi.Arg{abi.Scalar("state", abi.Ptr), abi.Scalar("m", abi.Ptr)},
		nil,
	)
	b := loong64.NewFunc("mix4", sig, 0)
	b.LoadArg("state", "R4").LoadArg("m", "R5")

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

	f := emit.NewFile("loong64 && !goexperiment.simd")
	f.Add(b.Func())
	if err := os.WriteFile("mix4_loong64.s", []byte(f.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote mix4_loong64.s")
}
