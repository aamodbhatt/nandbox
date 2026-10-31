package asm

import (
	"strconv"
	"strings"
)

// exprParser is a precedence-climbing evaluator for operand expressions:
// numbers (decimal, 0x, 0b), 'c' characters, symbols, $ (current address),
// unary - ~, and binary * / % + - << >> & ^ | with C precedence.
type exprParser struct {
	a    *assembler
	ln   int
	col  int
	s    string
	pos  int
	here int
	ok   bool
}

func (a *assembler) eval(ln int, ar arg, here int) (int, bool) {
	p := &exprParser{a: a, ln: ln, col: ar.col, s: ar.text, here: here, ok: true}
	v := p.binary(0)
	p.space()
	if p.ok && p.pos < len(p.s) {
		p.fail("unexpected %q in expression", p.s[p.pos:])
	}
	return v, p.ok
}

func (p *exprParser) fail(format string, args ...any) {
	if p.ok {
		p.a.errorf(p.ln, p.col+p.pos, format, args...)
	}
	p.ok = false
}

func (p *exprParser) space() {
	for p.pos < len(p.s) && (p.s[p.pos] == ' ' || p.s[p.pos] == '\t') {
		p.pos++
	}
}

var precedence = map[string]int{"|": 1, "^": 2, "&": 3, "<<": 4, ">>": 4, "+": 5, "-": 5, "*": 6, "/": 6, "%": 6}

func (p *exprParser) peekOp() string {
	p.space()
	for _, op := range []string{"<<", ">>", "|", "^", "&", "+", "-", "*", "/", "%"} {
		if strings.HasPrefix(p.s[p.pos:], op) {
			return op
		}
	}
	return ""
}

func (p *exprParser) binary(minPrec int) int {
	lhs := p.unary()
	for p.ok {
		op := p.peekOp()
		prec, isOp := precedence[op]
		if !isOp || prec <= minPrec {
			return lhs
		}
		p.pos += len(op)
		rhs := p.binary(prec)
		switch op {
		case "|":
			lhs |= rhs
		case "^":
			lhs ^= rhs
		case "&":
			lhs &= rhs
		case "<<":
			lhs <<= uint(rhs & 31)
		case ">>":
			lhs >>= uint(rhs & 31)
		case "+":
			lhs += rhs
		case "-":
			lhs -= rhs
		case "*":
			lhs *= rhs
		case "/", "%":
			if rhs == 0 {
				p.fail("division by zero")
				return 0
			}
			if op == "/" {
				lhs /= rhs
			} else {
				lhs %= rhs
			}
		}
	}
	return lhs
}

func (p *exprParser) unary() int {
	p.space()
	if p.pos >= len(p.s) {
		p.fail("missing value")
		return 0
	}
	switch c := p.s[p.pos]; {
	case c == '-':
		p.pos++
		return -p.unary()
	case c == '~':
		p.pos++
		return ^p.unary()
	case c == '(':
		p.pos++
		v := p.binary(0)
		p.space()
		if p.pos >= len(p.s) || p.s[p.pos] != ')' {
			p.fail("missing ')'")
			return 0
		}
		p.pos++
		return v
	case c == '$':
		p.pos++
		return p.here
	case c == '\'':
		if p.pos+2 < len(p.s) && p.s[p.pos+2] == '\'' {
			v := int(p.s[p.pos+1])
			p.pos += 3
			return v
		}
		p.fail("bad character literal")
		return 0
	case c >= '0' && c <= '9':
		start := p.pos
		for p.pos < len(p.s) && isIdent(p.s[p.pos]) {
			p.pos++
		}
		v, err := strconv.ParseInt(p.s[start:p.pos], 0, 32)
		if err != nil {
			p.pos = start
			p.fail("bad number %q", p.s[start:])
			return 0
		}
		return int(v)
	case isIdent(c):
		start := p.pos
		for p.pos < len(p.s) && isIdent(p.s[p.pos]) {
			p.pos++
		}
		name := p.s[start:p.pos]
		v, ok := p.a.syms[name]
		if !ok {
			p.pos = start
			p.fail("undefined symbol %q", name)
			return 0
		}
		return v
	}
	p.fail("unexpected %q", p.s[p.pos:])
	return 0
}
