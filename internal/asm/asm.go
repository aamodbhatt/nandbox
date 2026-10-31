// Package asm assembles NB8 assembly into a ROM image and an initial RAM
// image. It is a two-pass assembler with labels, constants, expressions,
// data directives and errors that carry line and column.
//
// Syntax summary (docs/ISA.md has the full reference):
//
//	.equ  NAME, expr      ; constant
//	.data / .code         ; switch section (data labels are RAM addresses)
//	.org  expr            ; move the location counter
//	.byte expr, ...       ; bytes in the data section
//	.ascii "text"         ; string bytes
//	.zero n               ; n zero bytes
//	label: add r0, 1      ; ALU ops: add adc sub sbc and or xor mov cmp tst
//	shr r1 | ld r0, [r2] | vld r0, [0x10] | st [r1], r0 | vst [r1], r0
//	jmp/jz/jnz/jc/jnc/jn/jnn target | jal r3, target | call t | ret | hlt | nop
package asm

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/aamodbhatt/nandbox/internal/isa"
)

// Error is one assembly problem at a source position (1-based).
type Error struct {
	Line, Col int
	Msg       string
}

func (e *Error) Error() string { return fmt.Sprintf("%d:%d: %s", e.Line, e.Col, e.Msg) }

// Program is assembled output.
type Program struct {
	Code    []uint16       // ROM image, one word per instruction
	Data    []uint8        // initial RAM image (bytes 0..len-1)
	Symbols map[string]int // labels and constants
	Lines   []int          // source line of each code word
}

// Option configures an assembly.
type Option func(*assembler)

// WithDefine predefines a symbol. It overrides an .equ of the same name, like
// a -D flag, so tests can shrink delay loops without editing sources.
func WithDefine(name string, value int) Option {
	return func(a *assembler) { a.defines[name] = value }
}

const (
	codeSize = 256
	dataSize = isa.IOBase
)

type line struct {
	no     int
	label  string
	labCol int
	op     string
	opCol  int
	args   []arg
}

type arg struct {
	text string
	col  int
}

type assembler struct {
	defines map[string]int
	syms    map[string]int
	errs    []error
}

// Assemble translates source into a program or returns every error found.
func Assemble(src string, opts ...Option) (*Program, error) {
	a := &assembler{defines: map[string]int{}, syms: map[string]int{}}
	for _, o := range opts {
		o(a)
	}
	for k, v := range a.defines {
		a.syms[k] = v
	}
	lines := a.parse(src)
	a.layout(lines)
	prog := a.emit(lines)
	if len(a.errs) > 0 {
		return nil, errors.Join(a.errs...)
	}
	return prog, nil
}

func (a *assembler) errorf(ln, col int, format string, args ...any) {
	a.errs = append(a.errs, &Error{Line: ln, Col: col, Msg: fmt.Sprintf(format, args...)})
}

// parse splits each line into label, operation and comma-separated operands.
func (a *assembler) parse(src string) []line {
	var out []line
	for i, raw := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		l := line{no: i + 1}
		text := stripComment(raw)
		pos := 0
		skip := func() {
			for pos < len(text) && (text[pos] == ' ' || text[pos] == '\t') {
				pos++
			}
		}
		skip()
		start := pos
		for pos < len(text) && isIdent(text[pos]) {
			pos++
		}
		if pos < len(text) && text[pos] == ':' && pos > start {
			l.label, l.labCol = text[start:pos], start+1
			pos++
			skip()
			start = pos
		} else {
			pos = start
		}
		for pos < len(text) && (isIdent(text[pos]) || text[pos] == '.') {
			pos++
		}
		l.op, l.opCol = strings.ToLower(text[start:pos]), start+1
		if l.op == "" && pos < len(text) && strings.TrimSpace(text[pos:]) != "" {
			a.errorf(l.no, pos+1, "expected an instruction or directive")
			continue
		}
		l.args = splitArgs(text[pos:], pos)
		if l.label != "" || l.op != "" {
			out = append(out, l)
		}
	}
	return out
}

func stripComment(s string) string {
	inStr := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			inStr = !inStr
		case '\'':
			if !inStr && i+2 < len(s) && s[i+2] == '\'' {
				i += 2
			}
		case ';':
			if !inStr {
				return s[:i]
			}
		}
	}
	return s
}

func isIdent(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func splitArgs(s string, offset int) []arg {
	var out []arg
	depth, inStr, start := 0, false, 0
	flush := func(end int) {
		t := s[start:end]
		trimmed := strings.TrimSpace(t)
		if trimmed != "" {
			out = append(out, arg{text: trimmed, col: offset + start + strings.Index(t, trimmed) + 1})
		}
		start = end + 1
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '[' || c == '(':
			depth++
		case c == ']' || c == ')':
			depth--
		case c == ',' && depth == 0:
			flush(i)
		}
	}
	flush(len(s))
	return out
}

// layout is pass one: it assigns addresses to labels and evaluates .equ.
func (a *assembler) layout(lines []line) {
	pc, dp, data := 0, 0, false
	for _, l := range lines {
		here := pc
		if data {
			here = dp
		}
		if l.label != "" {
			if _, dup := a.syms[l.label]; dup {
				a.errorf(l.no, l.labCol, "symbol %q already defined", l.label)
			}
			a.syms[l.label] = here
		}
		switch l.op {
		case "":
		case ".code", ".text":
			data = false
		case ".data":
			data = true
		case ".equ":
			if len(l.args) != 2 {
				a.errorf(l.no, l.opCol, ".equ needs NAME, value")
				continue
			}
			name := l.args[0].text
			if _, overridden := a.defines[name]; overridden {
				continue
			}
			if _, dup := a.syms[name]; dup {
				a.errorf(l.no, l.args[0].col, "symbol %q already defined", name)
				continue
			}
			if v, ok := a.eval(l.no, l.args[1], here); ok {
				a.syms[name] = v
			}
		case ".org":
			if v, ok := a.evalArgs(l, 1, here); ok {
				if data {
					dp = v[0]
				} else {
					pc = v[0]
				}
			}
		case ".byte", ".ascii", ".zero":
			if !data {
				a.errorf(l.no, l.opCol, "%s is only allowed in the .data section", l.op)
				continue
			}
			dp += a.dataLen(l)
		default:
			if data {
				a.errorf(l.no, l.opCol, "instruction %q in the .data section", l.op)
				continue
			}
			pc++
		}
	}
}

func (a *assembler) dataLen(l line) int {
	switch l.op {
	case ".byte":
		return len(l.args)
	case ".ascii":
		if len(l.args) == 1 {
			if s, err := strconv.Unquote(l.args[0].text); err == nil {
				return len(s)
			}
		}
		return 0
	default:
		if v, ok := a.evalArgs(l, 1, 0); ok {
			return v[0]
		}
		return 0
	}
}

func (a *assembler) evalArgs(l line, n, here int) ([]int, bool) {
	if len(l.args) != n {
		a.errorf(l.no, l.opCol, "%s takes %d operand(s), got %d", l.op, n, len(l.args))
		return nil, false
	}
	out := make([]int, n)
	for i, ar := range l.args {
		v, ok := a.eval(l.no, ar, here)
		if !ok {
			return nil, false
		}
		out[i] = v
	}
	return out, true
}

// emit is pass two: it encodes instructions and data.
func (a *assembler) emit(lines []line) *Program {
	p := &Program{Symbols: a.syms}
	code := make([]uint16, 0, codeSize)
	var lineOf []int
	data := make([]uint8, 0, dataSize)
	pc, dp, inData := 0, 0, false
	put := func(ln, col, v int) {
		if dp >= dataSize {
			a.errorf(ln, col, "data overflows RAM (%d bytes below the I/O ports)", dataSize)
			return
		}
		for len(data) <= dp {
			data = append(data, 0)
		}
		data[dp] = uint8(v)
		dp++
	}
	for _, l := range lines {
		switch l.op {
		case "", ".equ":
		case ".code", ".text":
			inData = false
		case ".data":
			inData = true
		case ".org":
			if v, ok := a.evalArgs(l, 1, pc); ok {
				if inData {
					dp = v[0]
				} else {
					for len(code) < v[0] && len(code) < codeSize {
						code = append(code, 0)
						lineOf = append(lineOf, l.no)
					}
					pc = v[0]
				}
			}
		case ".byte":
			for _, ar := range l.args {
				if v, ok := a.eval(l.no, ar, dp); ok {
					if v < -128 || v > 255 {
						a.errorf(l.no, ar.col, "byte value %d out of range", v)
					}
					put(l.no, ar.col, v)
				}
			}
		case ".ascii":
			s, err := strconv.Unquote(l.args[0].text)
			if len(l.args) != 1 || err != nil {
				a.errorf(l.no, l.opCol, ".ascii needs one quoted string")
				continue
			}
			for i := range len(s) {
				put(l.no, l.args[0].col, int(s[i]))
			}
		case ".zero":
			if v, ok := a.evalArgs(l, 1, dp); ok {
				for range v[0] {
					put(l.no, l.opCol, 0)
				}
			}
		default:
			if inData {
				continue
			}
			w, ok := a.encode(l, pc)
			if pc >= codeSize {
				a.errorf(l.no, l.opCol, "program exceeds %d instructions", codeSize)
			} else if ok {
				for len(code) < pc {
					code = append(code, 0)
					lineOf = append(lineOf, l.no)
				}
				code = append(code, w)
				lineOf = append(lineOf, l.no)
			}
			pc++
		}
	}
	p.Code, p.Data, p.Lines = code, data, lineOf
	return p
}

func parseReg(s string) (uint8, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "lr" {
		return 3, true
	}
	if len(s) == 2 && s[0] == 'r' && s[1] >= '0' && s[1] <= '3' {
		return s[1] - '0', true
	}
	return 0, false
}

// operand parses a register or an 8-bit immediate (expression).
func (a *assembler) operand(ln int, ar arg, here int) (imm bool, v uint8, ok bool) {
	if r, isReg := parseReg(ar.text); isReg {
		return false, r, true
	}
	n, ok := a.eval(ln, ar, here)
	if !ok {
		return false, 0, false
	}
	if n < -128 || n > 255 {
		a.errorf(ln, ar.col, "value %d does not fit in 8 bits", n)
		return false, 0, false
	}
	return true, uint8(n), true
}

func (a *assembler) memOperand(ln int, ar arg, here int) (bool, uint8, bool) {
	t := ar.text
	if len(t) < 2 || t[0] != '[' || t[len(t)-1] != ']' {
		a.errorf(ln, ar.col, "expected a memory operand like [r1] or [0x20], got %q", t)
		return false, 0, false
	}
	inner := arg{text: strings.TrimSpace(t[1 : len(t)-1]), col: ar.col + 1}
	return a.operand(ln, inner, here)
}

var aluOps = map[string]uint8{"add": isa.OpADD, "adc": isa.OpADC, "sub": isa.OpSUB, "sbc": isa.OpSBC,
	"and": isa.OpAND, "or": isa.OpOR, "xor": isa.OpXOR, "mov": isa.OpMOV, "cmp": isa.OpCMP, "tst": isa.OpTST}

var jumps = map[string]uint8{"jmp": isa.CondAlways, "jz": isa.CondZ, "je": isa.CondZ, "jc": isa.CondC, "jhs": isa.CondC,
	"jn": isa.CondN, "jmi": isa.CondN, "jnz": isa.CondInvert | isa.CondZ, "jne": isa.CondInvert | isa.CondZ,
	"jnc": isa.CondInvert | isa.CondC, "jlo": isa.CondInvert | isa.CondC, "jnn": isa.CondInvert | isa.CondN,
	"jpl": isa.CondInvert | isa.CondN}

func (a *assembler) want(l line, n int) bool {
	if len(l.args) != n {
		a.errorf(l.no, l.opCol, "%s takes %d operand(s), got %d", l.op, n, len(l.args))
		return false
	}
	return true
}

func (a *assembler) reg(l line, i int) (uint8, bool) {
	r, ok := parseReg(l.args[i].text)
	if !ok {
		a.errorf(l.no, l.args[i].col, "expected a register r0..r3, got %q", l.args[i].text)
	}
	return r, ok
}

func (a *assembler) encode(l line, pc int) (uint16, bool) {
	if op, ok := aluOps[l.op]; ok {
		if !a.want(l, 2) {
			return 0, false
		}
		rd, ok1 := a.reg(l, 0)
		imm, v, ok2 := a.operand(l.no, l.args[1], pc)
		return isa.Encode(op, imm, false, rd, v), ok1 && ok2
	}
	if cond, ok := jumps[l.op]; ok {
		if !a.want(l, 1) {
			return 0, false
		}
		imm, v, ok := a.operand(l.no, l.args[0], pc)
		return isa.EncodeJump(cond, imm, v), ok
	}
	switch l.op {
	case "shr":
		if !a.want(l, 1) {
			return 0, false
		}
		rd, ok := a.reg(l, 0)
		return isa.Encode(isa.OpSHR, false, false, rd, 0), ok
	case "ld", "vld":
		if !a.want(l, 2) {
			return 0, false
		}
		rd, ok1 := a.reg(l, 0)
		imm, v, ok2 := a.memOperand(l.no, l.args[1], pc)
		return isa.Encode(isa.OpLD, imm, l.op == "vld", rd, v), ok1 && ok2
	case "st", "vst":
		if !a.want(l, 2) {
			return 0, false
		}
		imm, v, ok1 := a.memOperand(l.no, l.args[0], pc)
		rd, ok2 := a.reg(l, 1)
		return isa.Encode(isa.OpST, imm, l.op == "vst", rd, v), ok1 && ok2
	case "jal":
		if !a.want(l, 2) {
			return 0, false
		}
		rd, ok1 := a.reg(l, 0)
		imm, v, ok2 := a.operand(l.no, l.args[1], pc)
		return isa.Encode(isa.OpJAL, imm, false, rd, v), ok1 && ok2
	case "call":
		if !a.want(l, 1) {
			return 0, false
		}
		imm, v, ok := a.operand(l.no, l.args[0], pc)
		return isa.Encode(isa.OpJAL, imm, false, 3, v), ok
	case "ret":
		return isa.EncodeJump(isa.CondAlways, false, 3), a.want(l, 0)
	case "hlt":
		return isa.Encode(isa.OpHLT, false, false, 0, 0), a.want(l, 0)
	case "nop":
		return isa.EncodeJump(isa.CondInvert|isa.CondAlways, true, 0), a.want(l, 0)
	}
	a.errorf(l.no, l.opCol, "unknown instruction %q", l.op)
	return 0, false
}

// SymbolNames returns symbols sorted by name (for listings).
func (p *Program) SymbolNames() []string {
	names := make([]string, 0, len(p.Symbols))
	for k := range p.Symbols {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
