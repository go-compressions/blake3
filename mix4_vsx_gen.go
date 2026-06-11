//go:build ignore

// Command gen produces mix4_ppc64le.s with go-asmgen: the BLAKE3 7-round 4-lane
// mixing kernel in VSX (POWER vector facility, 4 chunks per call, one per 32-bit
// lane). Memory-resident state like the SSE/LSX/RVV kernels: each quarter-round
// loads its four state words (each a 128-bit vector of 4 lanes), mixes, and
// stores them back.
//
// VSX↔VMX register aliasing: the AltiVec vector register Vn aliases the VSX
// register VS(32+n) — NOT VSn. So a load that feeds a V-register op must target
// VS(32+n): LXVD2X (R3)(R0), VS32 populates V0. Operating on V0 after loading
// into VS0 reads an uninitialised register (a bug the qemu run catches).
//
// Rotate: VSX has no rotate-right, only VRLW (rotate-left word) by a per-word
// count vector. BLAKE3's rotr(n) is rotl(32-n), so the four rotate-count vectors
// hold {16,20,24,25}. They are built once with VSPLTISW; since VRLW takes the
// count mod 32, the in-range signed immediates -16/-12/-8/-7 splat to the right
// low-5-bit values (same trick x/crypto's chacha20 ppc64 asm uses). The count
// vectors live in V28..V31 and the working words in V0..V4, so nothing spills.
package main

import (
	"fmt"
	"os"

	"github.com/go-asmgen/asmgen/abi"
	"github.com/go-asmgen/asmgen/emit"
	"github.com/go-asmgen/asmgen/ppc64"
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

// Rotate-count vectors. rotr(n) == rotl(32-n); VRLW ignores all but the low 5
// bits of the count, so a signed immediate that is ≡ (32-n) mod 32 works.
const (
	rotR16 = "V28" // rotr 16 -> rotl 16  (VSPLTISW $-16)
	rotR12 = "V29" // rotr 12 -> rotl 20  (VSPLTISW $-12)
	rotR8  = "V30" // rotr  8 -> rotl 24  (VSPLTISW $-8)
	rotR7  = "V31" // rotr  7 -> rotl 25  (VSPLTISW $-7)
)

// loadWord emits: V<dst> = mem[base + off] (one 128-bit vector = 4 lanes). The
// LXVD2X target must be the VS alias VS(32+dst) so the value lands in V<dst>.
func loadWord(b *ppc64.Builder, base string, off, dst int) {
	if off != 0 {
		b.Raw("MOVD $%d, R7", off)
		b.Raw("LXVD2X (%s)(R7), VS%d", base, 32+dst)
	} else {
		b.Raw("LXVD2X (%s)(R0), VS%d", base, 32+dst)
	}
}

func storeWord(b *ppc64.Builder, base string, off, src int) {
	if off != 0 {
		b.Raw("MOVD $%d, R7", off)
		b.Raw("STXVD2X VS%d, (%s)(R7)", 32+src, base)
	} else {
		b.Raw("STXVD2X VS%d, (%s)(R0)", 32+src, base)
	}
}

// gVec emits one quarter-round. R3 = state ptr, R4 = m ptr, R7 = offset scratch.
// Working words: V0(a) V1(b) V2(c) V3(d); V4 message. VADDUWM/VXOR/VRLW are the
// reversed-operand Plan 9 form: `OP src, src, dst`. VRLW value, count, dst.
func gVec(b *ppc64.Builder, a, bb, c, d, scx, scy int) {
	loadWord(b, "R3", a*16, 0)
	loadWord(b, "R3", bb*16, 1)
	loadWord(b, "R3", c*16, 2)
	loadWord(b, "R3", d*16, 3)
	b.Raw("VADDUWM V0, V1, V0")            // a += b
	loadWord(b, "R4", scx*16, 4)           // mx
	b.Raw("VADDUWM V0, V4, V0")            // a += mx
	b.Raw("VXOR V3, V0, V3")               // d ^= a
	b.Raw("VRLW V3, %s, V3", rotR16)       // d = rotr(d,16)
	b.Raw("VADDUWM V2, V3, V2")            // c += d
	b.Raw("VXOR V1, V2, V1")               // b ^= c
	b.Raw("VRLW V1, %s, V1", rotR12)       // b = rotr(b,12)
	b.Raw("VADDUWM V0, V1, V0")            // a += b
	loadWord(b, "R4", scy*16, 4)           // my
	b.Raw("VADDUWM V0, V4, V0")            // a += my
	b.Raw("VXOR V3, V0, V3")               // d ^= a
	b.Raw("VRLW V3, %s, V3", rotR8)        // d = rotr(d,8)
	b.Raw("VADDUWM V2, V3, V2")            // c += d
	b.Raw("VXOR V1, V2, V1")               // b ^= c
	b.Raw("VRLW V1, %s, V1", rotR7)        // b = rotr(b,7)
	storeWord(b, "R3", a*16, 0)
	storeWord(b, "R3", bb*16, 1)
	storeWord(b, "R3", c*16, 2)
	storeWord(b, "R3", d*16, 3)
}

func main() {
	sig := abi.LayoutArgs(
		[]abi.Arg{abi.Scalar("state", abi.Ptr), abi.Scalar("m", abi.Ptr)},
		nil,
	)
	b := ppc64.NewFunc("mix4", sig, 0)
	b.LoadArg("state", "R3").LoadArg("m", "R4")
	// Build the four rotate-count vectors once (VRLW masks to the low 5 bits).
	b.Raw("VSPLTISW $-16, %s", rotR16) // rotl 16
	b.Raw("VSPLTISW $-12, %s", rotR12) // rotl 20
	b.Raw("VSPLTISW $-8, %s", rotR8)   // rotl 24
	b.Raw("VSPLTISW $-7, %s", rotR7)   // rotl 25

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

	f := emit.NewFile("ppc64le && !goexperiment.simd")
	f.Add(b.Func())
	if err := os.WriteFile("mix4_ppc64le.s", []byte(f.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote mix4_ppc64le.s")
}
