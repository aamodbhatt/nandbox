# NB8: the nandbox 8-bit instruction set

NB8 is a small load/store ISA designed to be cheap in gates and easy to read
off the wires. The CPU that implements it is built entirely from NAND gates and
D flip-flops (`internal/hdl/std.go`); a Go reference emulator (`internal/isa`)
defines the same semantics and the two are tested in lockstep.

## Machine state

| State | Width | Notes |
|---|---|---|
| `r0`..`r3` | 4 × 8 bits | general purpose; `r3` doubles as the link register (`lr`) |
| `PC` | 8 bits | indexes a 256-word instruction ROM (Harvard: code and data are separate) |
| `Z` `N` `C` | 3 flags | zero, negative (bit 7), carry / not-borrow |
| phase | 1 bit | fetch or execute; every instruction takes exactly **2 clock cycles** |

There is no stack and no overflow flag. Subroutines save `lr` to RAM themselves
if they nest.

## Instruction format

Every instruction is one 16-bit word:

```
 15   12  11  10  9  8  7                0
+-------+---+---+------+------------------+
|  op   | I | S |  rd  |  imm8 / --- rs   |
+-------+---+---+------+------------------+
```

* `op` selects the operation.
* `I = 1`: the operand **B** is the 8-bit immediate. `I = 0`: B is register `rs` (bits 1..0).
* `S` selects the address space for loads and stores: data (0) or video (1).
* `rd` is the destination (or the register stored, for `st`).
* Jumps reuse bits 10..8 as a condition.

Operand B is used uniformly: it is the ALU's second input, the memory address
of loads and stores, and the jump target. That is why the datapath has one
operand multiplexer and no separate address adder.

## Opcodes

| op | Mnemonic | Effect | Flags |
|---|---|---|---|
| 0 | `add rd, B` | rd ← rd + B | Z N C |
| 1 | `adc rd, B` | rd ← rd + B + C | Z N C |
| 2 | `sub rd, B` | rd ← rd − B (C = 1 means no borrow) | Z N C |
| 3 | `sbc rd, B` | rd ← rd − B − !C | Z N C |
| 4 | `and rd, B` | rd ← rd & B | Z N, C ← 0 |
| 5 | `or rd, B` | rd ← rd \| B | Z N, C ← 0 |
| 6 | `xor rd, B` | rd ← rd ^ B | Z N, C ← 0 |
| 7 | `mov rd, B` | rd ← B | unchanged |
| 8 | `shr rd` | rd ← rd >> 1 | Z N, C ← old bit 0 |
| 9 | `ld rd, [B]` / `vld rd, [B]` | rd ← data[B] / video[B] | unchanged |
| A | `cmp rd, B` | rd − B, result discarded | Z N C |
| B | `st [B], rd` / `vst [B], rd` | data[B] / video[B] ← rd | unchanged |
| C | `tst rd, B` | rd & B, result discarded | Z N, C ← 0 |
| D | `j<cond> B` | if cond: PC ← B | unchanged |
| E | `jal rd, B` | rd ← return address; PC ← B | unchanged |
| F | `hlt` | stop fetching; the clock keeps running | unchanged |

`mov` deliberately leaves the flags alone, so a constant can be loaded between
a compare and its branch, or into an `adc` chain.

The ALU encoding is chosen so the hardware decodes it with a handful of gates:
bit 2 selects the logic unit (bits 1..0 pick AND/OR/XOR/pass-B), code 8 is the
shifter, and everything else is the adder with B inverted by bit 1 and the
carry flag used as carry-in when bit 0 is set (and bit 3 clear). `cmp` is the
adder in subtract mode (`A` = `1010`) and `tst` is the logic unit in AND mode
(`C` = `1100`), with write-back disabled by the control unit.

### Jump conditions (bits 10..8)

Bit 10 inverts; bits 9..8 select the flag.

| cond | Mnemonic | Taken when |
|---|---|---|
| 000 | `jmp` | always |
| 001 | `jz` / `je` | Z |
| 010 | `jc` / `jhs` | C (unsigned ≥ after `cmp`) |
| 011 | `jn` / `jmi` | N |
| 100 | `nop` | never |
| 101 | `jnz` / `jne` | !Z |
| 110 | `jnc` / `jlo` | !C (unsigned < after `cmp`) |
| 111 | `jnn` / `jpl` | !N |

Assembler conveniences: `call t` = `jal r3, t`, `ret` = `jmp r3`, `nop`.

## Timing

Every instruction takes two clock cycles:

1. **Fetch**: the ROM's registered output (which *is* the instruction register)
   captures `ROM[PC]`; `PC ← PC + 1`.
2. **Execute**: the ALU result, flags, jump target and stores are committed on
   the rising edge.

RAM, ROM and the screen are synchronous (like FPGA block RAM): a load presents
its address during execute and the data arrives on that edge, so the register
write-back of `ld` happens during the *next* fetch, while the instruction
register still holds the load. No stall, no forwarding, CPI = 2 for everything.

## Memory map

Two 256-byte address spaces, selected by the `S` bit:

| Space | Address | Contents |
|---|---|---|
| data | `0x00`–`0xEF` | RAM (initialised from the program's `.data` section) |
| data | `0xF0` | **INPUT** (read): joystick, bit 0 up, 1 down, 2 left, 3 right, 4 fire |
| data | `0xF1` | **RAND** (read): low byte of a 16-bit LFSR built from 16 DFFs and 3 XORs |
| data | `0xF2` | **SCORE** (read/write): two hex digits on two seven-segment displays |
| data | `0xF3` | **LEDS** (read/write): eight debug LEDs |
| video | `0x00`–`0xFF` | the 16×16 LED matrix, one byte per pixel, `row * 16 + col` |

The I/O ports are partially decoded (by address bits 1..0), so `0xF4`–`0xFF`
mirror them, as on many real 8-bit machines. Pixel values index a 16-colour
palette (`value & 15`): 0 is off, 1–4 and 6 are greens, 5 red, 7 orange,
8–15 a rainbow.

The LFSR steps on every clock cycle (taps 16, 14, 13, 11 with XNOR feedback,
so the all-zero power-on state is valid). Because loads complete one fetch
cycle later, `ld r0, [RAND]` reads the LFSR state two edges after the start of
the instruction; the reference emulator models exactly that.

## Assembler syntax

```asm
        .equ  SCORE, 0xF2      ; constants (expressions: + - * / % << >> & | ^ ~ ( ) $)
        .data                  ; data section: labels are RAM addresses
count:  .byte 0, 1, 'A'
msg:    .ascii "hello"
buf:    .zero 8
        .code                  ; code section: labels are ROM addresses
start:  mov r0, 10
loop:   sub r0, 1
        st  [SCORE], r0
        jnz loop
        hlt
```

Errors carry `line:column`. Predefined symbols (`asm.WithDefine`) override
`.equ`, like a `-D` flag; the tests use this to shorten delay loops.
