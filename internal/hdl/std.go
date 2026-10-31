package hdl

import "fmt"

// Std is the standard chip library, from single gates up to the CPU. Every
// chip bottoms out in NAND gates and DFFs.
type Std struct {
	Lib *Library

	Not, And, Or, Xor, Mux, DMux        *Chip
	Not8, And8, Or8, Xor8, Mux8         *Chip
	Mux4Way, Mux4Way8, Or8Way, DMux4Way *Chip
	HalfAdder, FullAdder, Add8, Inc8    *Chip
	ALU8                                *Chip
	Decoder2to4, Decoder4to16           *Chip
	Bit, Register8, DFF8, PC8, RegFile  *Chip
	Flags, Control, CPU                 *Chip
	LFSR16, Hex7Seg, IOBus              *Chip
}

// NewStd defines the whole standard library into a fresh Library.
func NewStd() *Std {
	s := &Std{Lib: NewLibrary()}
	s.gates()
	s.wide()
	s.arith()
	s.sequential()
	s.cpu()
	s.peripherals()
	return s
}

func one(b Bit) Bus { return Bus{b} }

// Call instantiates a chip with positional inputs (in declaration order) and
// returns its outputs in declaration order.
func (b *Builder) Call(c *Chip, label string, args ...Bus) []Bus {
	if len(args) != len(c.Inputs) {
		panic(fmt.Sprintf("hdl: %s: %s takes %d inputs, got %d", b.def.ID, c.ID, len(c.Inputs), len(args)))
	}
	ins := Pins{}
	for i, p := range c.Inputs {
		ins[p.Name] = args[i]
	}
	outs := b.Use(c, label, ins)
	res := make([]Bus, len(c.Outputs))
	for i, p := range c.Outputs {
		res[i] = outs[p.Name]
	}
	return res
}

// Gate helpers that instantiate library chips (keeping the hierarchy visible).
func (s *Std) not(b *Builder, x Bit) Bit    { return b.Call(s.Not, "", one(x))[0][0] }
func (s *Std) and(b *Builder, x, y Bit) Bit { return b.Call(s.And, "", one(x), one(y))[0][0] }
func (s *Std) or(b *Builder, x, y Bit) Bit  { return b.Call(s.Or, "", one(x), one(y))[0][0] }
func (s *Std) xor(b *Builder, x, y Bit) Bit { return b.Call(s.Xor, "", one(x), one(y))[0][0] }
func (s *Std) mux(b *Builder, x, y, sel Bit) Bit {
	return b.Call(s.Mux, "", one(x), one(y), one(sel))[0][0]
}
func (s *Std) mux8(b *Builder, label string, x, y Bus, sel Bit) Bus {
	return b.Call(s.Mux8, label, x, y, one(sel))[0]
}

// gates are drawn directly from NANDs: the textbook constructions.
func (s *Std) gates() {
	l := s.Lib
	s.Not = l.Define("Not", func(b *Builder) {
		in := b.In("in", 1)[0]
		b.Out("out", one(b.Nand(in, in)))
	})
	s.And = l.Define("And", func(b *Builder) {
		x, y := b.In("a", 1)[0], b.In("b", 1)[0]
		n := b.Nand(x, y)
		b.Out("out", one(b.Nand(n, n)))
	})
	s.Or = l.Define("Or", func(b *Builder) {
		x, y := b.In("a", 1)[0], b.In("b", 1)[0]
		b.Out("out", one(b.Nand(b.Nand(x, x), b.Nand(y, y))))
	})
	s.Xor = l.Define("Xor", func(b *Builder) {
		x, y := b.In("a", 1)[0], b.In("b", 1)[0]
		n := b.Nand(x, y)
		b.Out("out", one(b.Nand(b.Nand(x, n), b.Nand(y, n))))
	})
	s.Mux = l.Define("Mux", func(b *Builder) {
		x, y, sel := b.In("a", 1)[0], b.In("b", 1)[0], b.In("sel", 1)[0]
		ns := b.Nand(sel, sel)
		b.Out("out", one(b.Nand(b.Nand(x, ns), b.Nand(y, sel))))
	})
	s.DMux = l.Define("DMux", func(b *Builder) {
		in, sel := b.In("in", 1)[0], b.In("sel", 1)[0]
		ns := b.Nand(sel, sel)
		na, nb := b.Nand(in, ns), b.Nand(in, sel)
		b.Out("a", one(b.Nand(na, na)))
		b.Out("b", one(b.Nand(nb, nb)))
	})
}

func (s *Std) bitwise(name string, g *Chip) *Chip {
	return s.Lib.Define(name, func(b *Builder) {
		x, y := b.In("a", 8), b.In("b", 8)
		out := make(Bus, 8)
		for i := range out {
			out[i] = b.Call(g, fmt.Sprintf("%s%d", g.ID, i), one(x[i]), one(y[i]))[0][0]
		}
		b.Out("out", out)
	})
}

func (s *Std) wide() {
	l := s.Lib
	s.Not8 = l.Define("Not8", func(b *Builder) {
		in := b.In("in", 8)
		out := make(Bus, 8)
		for i := range out {
			out[i] = s.not(b, in[i])
		}
		b.Out("out", out)
	})
	s.And8 = s.bitwise("And8", s.And)
	s.Or8 = s.bitwise("Or8", s.Or)
	s.Xor8 = s.bitwise("Xor8", s.Xor)
	s.Mux8 = l.Define("Mux8", func(b *Builder) {
		x, y, sel := b.In("a", 8), b.In("b", 8), b.In("sel", 1)
		out := make(Bus, 8)
		for i := range out {
			out[i] = b.Call(s.Mux, fmt.Sprintf("mux%d", i), one(x[i]), one(y[i]), sel)[0][0]
		}
		b.Out("out", out)
	})
	s.Mux4Way = l.Define("Mux4Way", func(b *Builder) {
		x0, x1, x2, x3, sel := b.In("a", 1)[0], b.In("b", 1)[0], b.In("c", 1)[0], b.In("d", 1)[0], b.In("sel", 2)
		b.Out("out", one(s.mux(b, s.mux(b, x0, x1, sel[0]), s.mux(b, x2, x3, sel[0]), sel[1])))
	})
	s.Mux4Way8 = l.Define("Mux4Way8", func(b *Builder) {
		x0, x1, x2, x3, sel := b.In("a", 8), b.In("b", 8), b.In("c", 8), b.In("d", 8), b.In("sel", 2)
		lo := s.mux8(b, "lo", x0, x1, sel[0])
		hi := s.mux8(b, "hi", x2, x3, sel[0])
		b.Out("out", s.mux8(b, "pick", lo, hi, sel[1]))
	})
	s.Or8Way = l.Define("Or8Way", func(b *Builder) {
		in := b.In("in", 8)
		level := []Bit(in)
		for len(level) > 1 {
			var next []Bit
			for i := 0; i < len(level); i += 2 {
				next = append(next, s.or(b, level[i], level[i+1]))
			}
			level = next
		}
		b.Out("out", one(level[0]))
	})
	s.DMux4Way = l.Define("DMux4Way", func(b *Builder) {
		in, sel := b.In("in", 1), b.In("sel", 2)
		hi := b.Call(s.DMux, "hi", in, one(sel[1]))
		lo := b.Call(s.DMux, "lo0", hi[0], one(sel[0]))
		lo2 := b.Call(s.DMux, "lo1", hi[1], one(sel[0]))
		b.Out("a", lo[0])
		b.Out("b", lo[1])
		b.Out("c", lo2[0])
		b.Out("d", lo2[1])
	})
	s.Decoder2to4 = l.Define("Decoder2to4", func(b *Builder) {
		in := b.In("in", 2)
		n0, n1 := s.not(b, in[0]), s.not(b, in[1])
		b.Out("out", Bus{s.and(b, n0, n1), s.and(b, in[0], n1), s.and(b, n0, in[1]), s.and(b, in[0], in[1])})
	})
	s.Decoder4to16 = l.Define("Decoder4to16", func(b *Builder) {
		in := b.In("in", 4)
		lo := b.Call(s.Decoder2to4, "lo", in.Slice(0, 2))[0]
		hi := b.Call(s.Decoder2to4, "hi", in.Slice(2, 4))[0]
		out := make(Bus, 16)
		for v := range out {
			out[v] = s.and(b, hi[v>>2], lo[v&3])
		}
		b.Out("out", out)
	})
}

func (s *Std) arith() {
	l := s.Lib
	s.HalfAdder = l.Define("HalfAdder", func(b *Builder) {
		x, y := b.In("a", 1)[0], b.In("b", 1)[0]
		n := b.Nand(x, y)
		b.Out("sum", one(b.Nand(b.Nand(x, n), b.Nand(y, n))))
		b.Out("carry", one(b.Nand(n, n)))
	})
	// The nine-NAND full adder: two XORs sharing their first gate with the carry.
	s.FullAdder = l.Define("FullAdder", func(b *Builder) {
		x, y, cin := b.In("a", 1)[0], b.In("b", 1)[0], b.In("cin", 1)[0]
		n1 := b.Nand(x, y)
		h := b.Nand(b.Nand(x, n1), b.Nand(y, n1))
		n4 := b.Nand(h, cin)
		b.Out("sum", one(b.Nand(b.Nand(h, n4), b.Nand(cin, n4))))
		b.Out("cout", one(b.Nand(n1, n4)))
	})
	s.Add8 = l.Define("Add8", func(b *Builder) {
		x, y, c := b.In("a", 8), b.In("b", 8), b.In("cin", 1)
		sum := make(Bus, 8)
		for i := range sum {
			r := b.Call(s.FullAdder, fmt.Sprintf("fa%d", i), one(x[i]), one(y[i]), c)
			sum[i], c = r[0][0], r[1]
		}
		b.Out("sum", sum)
		b.Out("cout", c)
	})
	s.Inc8 = l.Define("Inc8", func(b *Builder) {
		in := b.In("in", 8)
		out := make(Bus, 8)
		c := One
		for i := range out {
			r := b.Call(s.HalfAdder, fmt.Sprintf("ha%d", i), one(in[i]), one(c))
			out[i], c = r[0][0], r[1][0]
		}
		b.Out("out", out)
	})
	// ALU8 implements isa.ALU: see the reference model there.
	s.ALU8 = l.Define("ALU8", func(b *Builder) {
		x, y, cin, op := b.In("a", 8), b.In("b", 8), b.In("cin", 1)[0], b.In("op", 4)
		inv := op[1]
		useC := s.and(b, op[0], s.not(b, op[3]))
		ci := s.mux(b, inv, cin, useC)
		yx := b.Call(s.Xor8, "invert", y, Bus{inv, inv, inv, inv, inv, inv, inv, inv})[0]
		add := b.Call(s.Add8, "adder", x, yx, one(ci))
		logic := b.Call(s.Mux4Way8, "logic",
			b.Call(s.And8, "and", x, y)[0], b.Call(s.Or8, "or", x, y)[0], b.Call(s.Xor8, "xor", x, y)[0], y, op.Slice(0, 2))[0]
		low3 := s.or(b, s.or(b, op[2], op[1]), op[0])
		isShr := s.and(b, op[3], s.not(b, low3))
		shr := Concat(x.Slice(1, 8), Bus{Zero})
		arith := s.mux8(b, "shift", add[0], shr, isShr)
		out := s.mux8(b, "result", arith, logic, op[2])
		b.Out("y", out)
		b.Out("z", one(s.not(b, b.Call(s.Or8Way, "zero", out)[0][0])))
		b.Out("n", one(out[7]))
		b.Out("c", one(s.and(b, s.mux(b, add[1][0], x[0], isShr), s.not(b, op[2]))))
	})
}

func (s *Std) sequential() {
	l := s.Lib
	s.Bit = l.Define("Bit", func(b *Builder) {
		in, load, clk := b.In("in", 1)[0], b.In("load", 1)[0], b.In("clk", 1)[0]
		q := b.Forward(1)
		d := s.mux(b, q[0], in, load)
		b.Resolve(q, one(b.DFF(d, clk)))
		b.Out("out", q)
	})
	s.Register8 = l.Define("Register8", func(b *Builder) {
		in, load, clk := b.In("in", 8), b.In("load", 1), b.In("clk", 1)
		out := make(Bus, 8)
		for i := range out {
			out[i] = b.Call(s.Bit, fmt.Sprintf("bit%d", i), one(in[i]), load, clk)[0][0]
		}
		b.Out("out", out)
	})
	s.DFF8 = l.Define("DFF8", func(b *Builder) {
		in, clk := b.In("in", 8), b.In("clk", 1)[0]
		out := make(Bus, 8)
		for i := range out {
			out[i] = b.DFF(in[i], clk)
		}
		b.Out("out", out)
	})
	s.PC8 = l.Define("PC8", func(b *Builder) {
		in, load, inc, reset, clk := b.In("in", 8), b.In("load", 1)[0], b.In("inc", 1)[0], b.In("reset", 1)[0], b.In("clk", 1)
		q := b.Forward(8)
		next := s.mux8(b, "inc?", q, b.Call(s.Inc8, "plus1", q)[0], inc)
		next = s.mux8(b, "load?", next, in, load)
		next = s.mux8(b, "reset?", next, Const(8, 0), reset)
		b.Resolve(q, b.Call(s.DFF8, "pc", next, clk)[0])
		b.Out("out", q)
	})
	s.RegFile = l.Define("RegFile", func(b *Builder) {
		we, wsel, wdata := b.In("we", 1)[0], b.In("wsel", 2), b.In("wdata", 8)
		asel, bsel, clk := b.In("asel", 2), b.In("bsel", 2), b.In("clk", 1)
		dec := b.Call(s.Decoder2to4, "wdec", wsel)[0]
		var regs [4]Bus
		for i := range regs {
			regs[i] = b.Call(s.Register8, fmt.Sprintf("r%d", i), wdata, one(s.and(b, we, dec[i])), clk)[0]
		}
		b.Out("a", b.Call(s.Mux4Way8, "readA", regs[0], regs[1], regs[2], regs[3], asel)[0])
		b.Out("b", b.Call(s.Mux4Way8, "readB", regs[0], regs[1], regs[2], regs[3], bsel)[0])
	})
	s.Flags = l.Define("Flags", func(b *Builder) {
		z, n, c, load, clk := b.In("z", 1), b.In("n", 1), b.In("c", 1), b.In("load", 1), b.In("clk", 1)
		b.Out("Z", b.Call(s.Bit, "Z", z, load, clk)[0])
		b.Out("N", b.Call(s.Bit, "N", n, load, clk)[0])
		b.Out("C", b.Call(s.Bit, "C", c, load, clk)[0])
	})
}

func (s *Std) cpu() {
	l := s.Lib
	// Control decodes the opcode one-hot and sequences the fetch/execute phases.
	s.Control = l.Define("Control", func(b *Builder) {
		op, cond := b.In("op", 4), b.In("cond", 3)
		z, c, n := b.In("Z", 1)[0], b.In("C", 1)[0], b.In("N", 1)[0]
		reset, clk := b.In("reset", 1)[0], b.In("clk", 1)[0]
		lines := b.Call(s.Decoder4to16, "opcode", op)[0]
		isLD, isST, isJ, isJAL, isHLT := lines[9], lines[11], lines[13], lines[14], lines[15]
		wbALU := s.or(b, s.not(b, op[3]), lines[8])
		// Every ALU operation except MOV updates the flags, so constants can be
		// loaded between a compare and its branch, or into an ADC chain.
		setsFlags := s.or(b, s.and(b, wbALU, s.not(b, lines[7])), s.or(b, lines[10], lines[12]))
		flag := b.Call(s.Mux4Way, "cond", one(One), one(z), one(c), one(n), cond.Slice(0, 2))[0][0]
		taken := s.xor(b, flag, cond[2])
		jump := s.or(b, isJAL, s.and(b, isJ, taken))
		exec := b.Forward(1)
		fetch := s.not(b, exec[0])
		notReset := s.not(b, reset)
		b.Resolve(exec, one(b.DFF(s.and(b, fetch, notReset), clk)))
		notHalt := s.not(b, isHLT)
		writeExec := s.and(b, exec[0], s.or(b, wbALU, isJAL))
		b.Out("regWE", one(s.and(b, notReset, s.or(b, writeExec, s.and(b, fetch, isLD)))))
		b.Out("flagsWE", one(s.and(b, exec[0], setsFlags)))
		b.Out("pcLoad", one(s.and(b, exec[0], jump)))
		b.Out("pcInc", one(s.and(b, fetch, notHalt)))
		b.Out("fetch", one(s.and(b, fetch, s.or(b, notHalt, reset))))
		b.Out("store", one(s.and(b, exec[0], isST)))
		b.Out("wbMem", one(isLD))
		b.Out("wbPC", one(isJAL))
		b.Out("exec", exec)
		b.Out("halted", one(isHLT))
	})
	s.CPU = l.Define("CPU", func(b *Builder) {
		clk, reset, instr, rdata := b.In("clk", 1), b.In("reset", 1), b.In("instr", 16), b.In("rdata", 8)
		op, imm, rs := instr.Slice(12, 16), instr.Slice(0, 8), instr.Slice(0, 2)
		rd, cond := instr.Slice(8, 10), instr.Slice(8, 11)
		flags := b.Forward(3)
		wb := b.Forward(8)
		ctl := b.Use(s.Control, "control", Pins{"op": op, "cond": cond, "Z": flags.Slice(0, 1),
			"N": flags.Slice(1, 2), "C": flags.Slice(2, 3), "reset": reset, "clk": clk})
		regs := b.Use(s.RegFile, "regs", Pins{"we": ctl["regWE"], "wsel": rd, "wdata": wb, "asel": rd, "bsel": rs, "clk": clk})
		bop := s.mux8(b, "operandB", regs["b"], imm, instr[11])
		alu := b.Use(s.ALU8, "alu", Pins{"a": regs["a"], "b": bop, "cin": flags.Slice(2, 3), "op": op})
		fl := b.Use(s.Flags, "flags", Pins{"z": alu["z"], "n": alu["n"], "c": alu["c"], "load": ctl["flagsWE"], "clk": clk})
		b.Resolve(flags, Concat(fl["Z"], fl["N"], fl["C"]))
		pc := b.Use(s.PC8, "pc", Pins{"in": bop, "load": ctl["pcLoad"], "inc": ctl["pcInc"], "reset": reset, "clk": clk})
		wbData := s.mux8(b, "loadData?", alu["y"], rdata, ctl["wbMem"][0])
		b.Resolve(wb, s.mux8(b, "returnAddr?", wbData, pc["out"], ctl["wbPC"][0]))
		b.Out("pc", pc["out"])
		b.Out("fetch", ctl["fetch"])
		b.Out("addr", bop)
		b.Out("wdata", regs["a"])
		b.Out("store", ctl["store"])
		b.Out("space", instr.Slice(10, 11))
		b.Out("exec", ctl["exec"])
		b.Out("halted", ctl["halted"])
	})
}

// segOff lists, per segment a..g, the hex digits for which it is dark.
var segOff = [7][]int{
	{0x1, 0x4, 0xB, 0xD},
	{0x5, 0x6, 0xB, 0xC, 0xE, 0xF},
	{0x2, 0xC, 0xE, 0xF},
	{0x1, 0x4, 0x7, 0xA, 0xF},
	{0x1, 0x3, 0x4, 0x5, 0x7, 0x9},
	{0x1, 0x2, 0x3, 0x7, 0xD},
	{0x0, 0x1, 0x7, 0xC},
}

func (s *Std) peripherals() {
	l := s.Lib
	s.LFSR16 = l.Define("LFSR16", func(b *Builder) {
		clk := b.In("clk", 1)[0]
		q := b.Forward(16)
		fb := s.not(b, s.xor(b, s.xor(b, q[15], q[13]), s.xor(b, q[12], q[10])))
		next := Concat(Bus{fb}, q.Slice(0, 15))
		out := make(Bus, 16)
		for i := range out {
			out[i] = b.DFF(next[i], clk)
		}
		b.Resolve(q, out)
		b.Out("out", q)
	})
	// Hex7Seg: an active-low 4-to-16 decoder; each segment is the AND of the
	// lines of the digits that keep it dark.
	s.Hex7Seg = l.Define("Hex7Seg", func(b *Builder) {
		in := b.In("in", 4)
		lo := b.Call(s.Decoder2to4, "lo", in.Slice(0, 2))[0]
		hi := b.Call(s.Decoder2to4, "hi", in.Slice(2, 4))[0]
		var darkN [16]Bit
		for v := range darkN {
			darkN[v] = b.Nand(hi[v>>2], lo[v&3])
		}
		for seg, digits := range segOff {
			acc := darkN[digits[0]]
			for _, d := range digits[1:] {
				acc = s.and(b, acc, darkN[d])
			}
			b.Out(string(rune('a'+seg)), one(acc))
		}
	})
	s.IOBus = l.Define("IOBus", func(b *Builder) {
		clk, addr, wdata := b.In("clk", 1), b.In("addr", 8), b.In("wdata", 8)
		store, space := b.In("store", 1)[0], b.In("space", 1)[0]
		ram, vram, joy := b.In("ram", 8), b.In("vram", 8), b.In("joy", 5)
		isIO := s.and(b, s.and(b, addr[7], addr[6]), s.and(b, addr[5], addr[4]))
		dataStore := s.and(b, store, s.not(b, space))
		ioStore := s.and(b, dataStore, isIO)
		scoreWE := s.and(b, ioStore, s.and(b, addr[1], s.not(b, addr[0])))
		ledsWE := s.and(b, ioStore, s.and(b, addr[1], addr[0]))
		score := b.Call(s.Register8, "score", wdata, one(scoreWE), clk)[0]
		leds := b.Call(s.Register8, "leds", wdata, one(ledsWE), clk)[0]
		rnd := b.Call(s.LFSR16, "random", clk)[0]
		port := b.Call(s.Mux4Way8, "ports", Concat(joy, Const(3, 0)), rnd.Slice(0, 8), score, leds, addr.Slice(0, 2))[0]
		data := s.mux8(b, "io?", ram, port, isIO)
		b.Out("rdata", s.mux8(b, "video?", data, vram, space))
		b.Out("ramWE", one(s.and(b, dataStore, s.not(b, isIO))))
		b.Out("vramWE", one(s.and(b, store, space)))
		b.Out("score", score)
		b.Out("leds", leds)
	})
}
