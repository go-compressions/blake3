# go1.27 + Zvbb RVV variant (experimental — not for stable)

This branch builds the BLAKE3 RVV mix kernel with `vror.vi` (Zvbb) for the
rotations instead of the base-RVV `VSRLVI + VSLLVI + VORVV` emulation on `main`.

- **Instruction count (hard, static fact):** 672 rotate-emulation ops → **224
  `VRORVI`** (3 ops → 1 per rotate; 448 fewer instructions per Mix4 call).
- **Requires:** Go **1.27+** (the assembler gained `VRORVI` in CL/commit
  58efaf3859; not in any release ≤ go1.26.4) **and** a CPU with the **Zvbb**
  extension at runtime. The stable emulated kernel stays on `main` (Go 1.20+, any
  RVV). Built/verified here with `gotip` (go1.27-devel).
- **Correctness:** verified identical BLAKE3 output on qemu riscv64 + Zvbb.
- **Performance — NOT measured on real hardware, and deliberately NOT claimed:**
  qemu-user is TCG (a functional/instruction-count emulator, not cycle-accurate).
  Its `vror.vi` helper is not cheaper than three simple ops, so under qemu this
  variant shows **no speedup** (within noise / slightly slower) — a TCG artifact,
  unrepresentative of real Zvbb silicon. A real timing comparison needs RVV+Zvbb
  hardware (or a cycle-accurate model); the objective evidence today is the
  instruction-count reduction above.
