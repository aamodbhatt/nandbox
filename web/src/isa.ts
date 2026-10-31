// NB8 disassembler for the dock's instruction readout (mirrors internal/isa).
const MNEMONICS = ["add", "adc", "sub", "sbc", "and", "or", "xor", "mov", "shr", "ld", "cmp", "st", "tst", "j", "jal", "hlt"];
const CONDS = ["jmp", "jz", "jc", "jn", "nop", "jnz", "jnc", "jnn"];

const hex = (v: number): string => `0x${v.toString(16).padStart(2, "0")}`;

export function disassemble(w: number): string {
  const op = (w >> 12) & 15;
  const imm = ((w >> 11) & 1) === 1;
  const space = ((w >> 10) & 1) === 1;
  const rd = (w >> 8) & 3;
  const src = imm ? hex(w & 0xff) : `r${w & 3}`;
  switch (op) {
    case 8:
      return `shr r${rd}`;
    case 9:
      return `${space ? "vld" : "ld"} r${rd}, [${src}]`;
    case 11:
      return `${space ? "vst" : "st"} [${src}], r${rd}`;
    case 13: {
      const cond = (w >> 8) & 7;
      return cond === 4 ? "nop" : `${CONDS[cond]} ${src}`;
    }
    case 14:
      return `jal r${rd}, ${src}`;
    case 15:
      return "hlt";
  }
  return `${MNEMONICS[op]} r${rd}, ${src}`;
}
