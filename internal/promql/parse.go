package promql

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type tok struct {
	k   string // num, dur, str, id, op, eof
	s   string
	pos int
}

func isIdStart(c byte) bool {
	return c == '_' || c == ':' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func lex(src string) ([]tok, error) {
	var out []tok
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case isDigit(c) || (c == '.' && i+1 < len(src) && isDigit(src[i+1])):
			j := i
			if c == '0' && i+1 < len(src) && (src[i+1] == 'x' || src[i+1] == 'X') {
				j += 2
				for j < len(src) && strings.IndexByte("0123456789abcdefABCDEF", src[j]) >= 0 {
					j++
				}
				out = append(out, tok{"num", src[i:j], i})
				i = j
				continue
			}
			for j < len(src) && (isDigit(src[j]) || src[j] == '.') {
				j++
			}
			if j < len(src) && (src[j] == 'e' || src[j] == 'E') && j+1 < len(src) && (isDigit(src[j+1]) || ((src[j+1] == '+' || src[j+1] == '-') && j+2 < len(src) && isDigit(src[j+2]))) {
				j += 2
				for j < len(src) && isDigit(src[j]) {
					j++
				}
				out = append(out, tok{"num", src[i:j], i})
				i = j
				continue
			}
			if j < len(src) && strings.IndexByte("smhdwy", src[j]) >= 0 {
				// duration such as 5m or 1h30m
				for j < len(src) && (isDigit(src[j]) || strings.IndexByte("smhdwy", src[j]) >= 0) {
					j++
				}
				out = append(out, tok{"dur", src[i:j], i})
				i = j
				continue
			}
			out = append(out, tok{"num", src[i:j], i})
			i = j
		case isIdStart(c):
			j := i
			for j < len(src) && (isIdStart(src[j]) || isDigit(src[j])) {
				j++
			}
			out = append(out, tok{"id", src[i:j], i})
			i = j
		case c == '"' || c == '\'' || c == '`':
			j := i + 1
			var sb strings.Builder
			for j < len(src) && src[j] != c {
				if src[j] == '\\' && c != '`' && j+1 < len(src) {
					j++
					switch src[j] {
					case 'n':
						sb.WriteByte('\n')
					case 't':
						sb.WriteByte('\t')
					default:
						sb.WriteByte(src[j])
					}
				} else {
					sb.WriteByte(src[j])
				}
				j++
			}
			if j >= len(src) {
				return nil, fmt.Errorf("unterminated string at %d", i)
			}
			out = append(out, tok{"str", sb.String(), i})
			i = j + 1
		default:
			two := ""
			if i+1 < len(src) {
				two = src[i : i+2]
			}
			switch two {
			case "==", "!=", "=~", "!~", "<=", ">=":
				out = append(out, tok{"op", two, i})
				i += 2
				continue
			}
			if strings.IndexByte("+-*/%^<>=(){}[],@:", c) >= 0 {
				out = append(out, tok{"op", string(c), i})
				i++
				continue
			}
			return nil, fmt.Errorf("unexpected character %q at %d", c, i)
		}
	}
	out = append(out, tok{"eof", "", len(src)})
	return out, nil
}

// ParseDuration reads 5m, 1h30m, 15s, 2d, 1w, 1y.
func ParseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	var total time.Duration
	rest := s
	for rest != "" {
		i := 0
		for i < len(rest) && isDigit(rest[i]) {
			i++
		}
		if i == 0 || i == len(rest) {
			return 0, fmt.Errorf("bad duration %q", s)
		}
		n, _ := strconv.ParseInt(rest[:i], 10, 64)
		j := i + 1
		var u time.Duration
		switch rest[i] {
		case 's':
			u = time.Second
		case 'm':
			if j < len(rest) && rest[j] == 's' {
				u = time.Millisecond
				j++
			} else {
				u = time.Minute
			}
		case 'h':
			u = time.Hour
		case 'd':
			u = 24 * time.Hour
		case 'w':
			u = 7 * 24 * time.Hour
		case 'y':
			u = 365 * 24 * time.Hour
		default:
			return 0, fmt.Errorf("bad duration %q", s)
		}
		total += time.Duration(n) * u
		rest = rest[j:]
	}
	return total, nil
}

type parser struct {
	t []tok
	i int
}

func (p *parser) peek() tok { return p.t[p.i] }
func (p *parser) next() tok { t := p.t[p.i]; p.i++; return t }
func (p *parser) isOp(s string) bool {
	t := p.peek()
	return t.k == "op" && t.s == s
}
func (p *parser) isId(s string) bool {
	t := p.peek()
	return t.k == "id" && strings.EqualFold(t.s, s)
}
func (p *parser) expectOp(s string) error {
	if !p.isOp(s) {
		return p.errf("expected %q", s)
	}
	p.i++
	return nil
}
func (p *parser) errf(f string, a ...any) error {
	t := p.peek()
	got := t.s
	if t.k == "eof" {
		got = "end of input"
	}
	return fmt.Errorf("parse error at %d (near %q): %s", t.pos, got, fmt.Sprintf(f, a...))
}

// Parse reads a PromQL expression.
func Parse(src string) (Expr, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{t: toks}
	e, err := p.expr(0)
	if err != nil {
		return nil, err
	}
	if p.peek().k != "eof" {
		return nil, p.errf("unexpected input")
	}
	return e, nil
}

// precedence: or(1) and/unless(2) comparison(3) +-(4) */%(5) ^(6)
func binPrec(t tok) int {
	if t.k == "op" {
		switch t.s {
		case "==", "!=", "<", ">", "<=", ">=":
			return 3
		case "+", "-":
			return 4
		case "*", "/", "%":
			return 5
		case "^":
			return 6
		}
	}
	if t.k == "id" {
		switch strings.ToLower(t.s) {
		case "or":
			return 1
		case "and", "unless":
			return 2
		case "atan2":
			return 5
		}
	}
	return 0
}

func (p *parser) expr(min int) (Expr, error) {
	left, err := p.unary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		pr := binPrec(t)
		if pr == 0 || pr < min {
			return left, nil
		}
		p.i++
		op := strings.ToLower(t.s)
		b := &Binary{Op: op, L: left}
		if pr == 3 && p.isId("bool") {
			p.i++
			b.Bool = true
		}
		if p.isId("on") || p.isId("ignoring") {
			b.On = strings.EqualFold(p.next().s, "on")
			b.Ignoring = !b.On
			if b.Labels, err = p.labelList(); err != nil {
				return nil, err
			}
			if p.isId("group_left") || p.isId("group_right") {
				return nil, p.errf("group_left and group_right are not supported")
			}
		}
		next := pr + 1
		if op == "^" {
			next = pr // right associative
		}
		if b.R, err = p.expr(next); err != nil {
			return nil, err
		}
		left = b
	}
}

func (p *parser) unary() (Expr, error) {
	if p.isOp("-") || p.isOp("+") {
		neg := p.next().s == "-"
		x, err := p.expr(6) // binds looser than ^: -2^2 = -4
		if err != nil {
			return nil, err
		}
		if n, ok := x.(*NumberLit); ok && neg {
			return &NumberLit{-n.V}, nil
		}
		return &Unary{Neg: neg, X: x}, nil
	}
	return p.postfix()
}

func (p *parser) labelList() ([]string, error) {
	if err := p.expectOp("("); err != nil {
		return nil, err
	}
	var out []string
	for !p.isOp(")") {
		t := p.next()
		if t.k != "id" {
			p.i--
			return nil, p.errf("expected a label name")
		}
		out = append(out, t.s)
		if p.isOp(",") {
			p.i++
		} else if !p.isOp(")") {
			return nil, p.errf("expected , or )")
		}
	}
	p.i++
	return out, nil
}

var aggOps = map[string]bool{"sum": true, "avg": true, "min": true, "max": true, "count": true, "group": true,
	"stddev": true, "stdvar": true, "topk": true, "bottomk": true, "quantile": true}

func (p *parser) postfix() (Expr, error) {
	e, err := p.primary()
	if err != nil {
		return nil, err
	}
	if p.isOp("[") {
		vs, ok := e.(*VectorSel)
		if !ok {
			return nil, p.errf("a range can only follow a metric selector (subqueries are not supported)")
		}
		p.i++
		d := p.next()
		if d.k != "dur" {
			p.i--
			return nil, p.errf("expected a duration such as 5m")
		}
		dur, err := ParseDuration(d.s)
		if err != nil {
			return nil, err
		}
		if p.isOp(":") {
			return nil, p.errf("subqueries are not supported")
		}
		if err := p.expectOp("]"); err != nil {
			return nil, err
		}
		e = &MatrixSel{VS: vs, Range: dur}
	}
	if p.isId("offset") {
		var vs *VectorSel
		switch x := e.(type) {
		case *VectorSel:
			vs = x
		case *MatrixSel:
			vs = x.VS
		default:
			return nil, p.errf("offset needs a metric selector")
		}
		p.i++
		neg := false
		if p.isOp("-") {
			p.i++
			neg = true
		}
		d := p.next()
		if d.k != "dur" {
			p.i--
			return nil, p.errf("expected a duration after offset")
		}
		dur, err := ParseDuration(d.s)
		if err != nil {
			return nil, err
		}
		if neg {
			dur = -dur
		}
		vs.Offset = dur
	}
	if p.isOp("@") {
		return nil, p.errf("the @ modifier is not supported")
	}
	return e, nil
}

func (p *parser) primary() (Expr, error) {
	t := p.peek()
	switch t.k {
	case "num":
		p.i++
		if v, err := strconv.ParseFloat(t.s, 64); err == nil {
			return &NumberLit{v}, nil
		}
		if v, err := strconv.ParseInt(t.s, 0, 64); err == nil {
			return &NumberLit{float64(v)}, nil
		}
		return nil, fmt.Errorf("bad number %q", t.s)
	case "str":
		p.i++
		return &StringLit{t.s}, nil
	case "op":
		switch t.s {
		case "(":
			p.i++
			e, err := p.expr(0)
			if err != nil {
				return nil, err
			}
			if err := p.expectOp(")"); err != nil {
				return nil, err
			}
			return e, nil
		case "{":
			return p.selector("")
		}
	case "id":
		low := strings.ToLower(t.s)
		if low == "inf" {
			p.i++
			return &NumberLit{math.Inf(1)}, nil
		}
		if low == "nan" {
			p.i++
			return &NumberLit{math.NaN()}, nil
		}
		if aggOps[low] {
			return p.aggregate(low)
		}
		if p.t[p.i+1].k == "op" && p.t[p.i+1].s == "(" {
			if _, ok := funcs[low]; !ok {
				return nil, p.errf("unknown function %q", t.s)
			}
			p.i += 2
			var args []Expr
			for !p.isOp(")") {
				a, err := p.expr(0)
				if err != nil {
					return nil, err
				}
				args = append(args, a)
				if p.isOp(",") {
					p.i++
				} else if !p.isOp(")") {
					return nil, p.errf("expected , or )")
				}
			}
			p.i++
			return &Call{Func: low, Args: args}, nil
		}
		p.i++
		return p.selector(t.s)
	}
	return nil, p.errf("unexpected token")
}

func (p *parser) selector(name string) (Expr, error) {
	vs := &VectorSel{Name: name}
	if p.isOp("{") {
		p.i++
		for !p.isOp("}") {
			n := p.next()
			if n.k != "id" {
				p.i--
				return nil, p.errf("expected a label name")
			}
			o := p.next()
			if o.k != "op" || (o.s != "=" && o.s != "!=" && o.s != "=~" && o.s != "!~") {
				p.i--
				return nil, p.errf("expected =, !=, =~ or !~")
			}
			v := p.next()
			if v.k != "str" {
				p.i--
				return nil, p.errf("expected a quoted value")
			}
			m := Matcher{n.s, o.s, v.s}
			if o.s == "=~" || o.s == "!~" {
				if _, err := regexp.Compile("^(?:" + v.s + ")$"); err != nil {
					return nil, fmt.Errorf("bad regular expression %q: %v", v.s, err)
				}
			}
			vs.Matchers = append(vs.Matchers, m)
			if p.isOp(",") {
				p.i++
			} else if !p.isOp("}") {
				return nil, p.errf("expected , or }")
			}
		}
		p.i++
	}
	if name == "" && len(vs.Matchers) == 0 {
		return nil, p.errf("a selector needs a name or at least one matcher")
	}
	return vs, nil
}

func (p *parser) aggregate(op string) (Expr, error) {
	p.i++
	a := &Agg{Op: op}
	grouping := func() error {
		if p.isId("by") || p.isId("without") {
			a.Without = strings.EqualFold(p.next().s, "without")
			l, err := p.labelList()
			if err != nil {
				return err
			}
			a.By, a.Grouping = l, true
		}
		return nil
	}
	if err := grouping(); err != nil {
		return nil, err
	}
	if err := p.expectOp("("); err != nil {
		return nil, err
	}
	first, err := p.expr(0)
	if err != nil {
		return nil, err
	}
	if op == "topk" || op == "bottomk" || op == "quantile" {
		if err := p.expectOp(","); err != nil {
			return nil, err
		}
		a.Param = first
		if first, err = p.expr(0); err != nil {
			return nil, err
		}
	}
	a.Arg = first
	if err := p.expectOp(")"); err != nil {
		return nil, err
	}
	if !a.Grouping {
		if err := grouping(); err != nil {
			return nil, err
		}
	}
	return a, nil
}
