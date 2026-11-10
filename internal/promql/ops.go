package promql

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

func isCmp(op string) bool {
	switch op {
	case "==", "!=", "<", ">", "<=", ">=":
		return true
	}
	return false
}

func isSet(op string) bool { return op == "and" || op == "or" || op == "unless" }

func arith(op string, a, b float64) float64 {
	switch op {
	case "+":
		return a + b
	case "-":
		return a - b
	case "*":
		return a * b
	case "/":
		return a / b
	case "%":
		return math.Mod(a, b)
	case "^":
		return math.Pow(a, b)
	case "atan2":
		return math.Atan2(a, b)
	}
	return math.NaN()
}

func compare(op string, a, b float64) bool {
	switch op {
	case "==":
		return a == b
	case "!=":
		return a != b
	case "<":
		return a < b
	case ">":
		return a > b
	case "<=":
		return a <= b
	case ">=":
		return a >= b
	}
	return false
}

// signature is the matching key of a sample for a binary operator.
func signature(l Labels, b *Binary) string {
	m := Labels{}
	switch {
	case b.On:
		for _, k := range b.Labels {
			if v, ok := l[k]; ok {
				m[k] = v
			}
		}
	default:
		for k, v := range l {
			m[k] = v
		}
		delete(m, "__name__")
		for _, k := range b.Labels {
			delete(m, k)
		}
	}
	return labelString(m)
}

func resultLabels(l Labels, b *Binary, dropN bool) Labels {
	o := Labels{}
	for k, v := range l {
		o[k] = v
	}
	if dropN {
		delete(o, "__name__")
	}
	if b.On {
		keep := map[string]bool{}
		for _, k := range b.Labels {
			keep[k] = true
		}
		for k := range o {
			if !keep[k] {
				delete(o, k)
			}
		}
	} else if b.Ignoring {
		for _, k := range b.Labels {
			delete(o, k)
		}
	}
	return o
}

func (e *evaluator) binary(b *Binary, t int64) (Value, error) {
	l, err := e.eval(b.L, t)
	if err != nil {
		return l, err
	}
	r, err := e.eval(b.R, t)
	if err != nil {
		return r, err
	}
	if l.IsMat || r.IsMat {
		return Value{}, fmt.Errorf("binary operators do not work on range vectors")
	}
	cmp := isCmp(b.Op)
	if isSet(b.Op) {
		if l.Scalar || r.Scalar {
			return Value{}, fmt.Errorf("set operator %q needs vectors on both sides", b.Op)
		}
		return setOp(b, l.Vec, r.Vec), nil
	}
	switch {
	case l.Scalar && r.Scalar:
		if cmp {
			if !b.Bool {
				return Value{}, fmt.Errorf("comparisons between scalars must use the bool modifier")
			}
			f := 0.0
			if compare(b.Op, l.F, r.F) {
				f = 1
			}
			return Value{Scalar: true, F: f}, nil
		}
		return Value{Scalar: true, F: arith(b.Op, l.F, r.F)}, nil
	case l.Scalar || r.Scalar:
		vec, sc, scalarLeft := l.Vec, r.F, false
		if l.Scalar {
			vec, sc, scalarLeft = r.Vec, l.F, true
		}
		out := make([]Sample, 0, len(vec))
		for _, s := range vec {
			a, c := s.V, sc
			if scalarLeft {
				a, c = sc, s.V
			}
			if cmp {
				ok := compare(b.Op, a, c)
				if b.Bool {
					v := 0.0
					if ok {
						v = 1
					}
					out = append(out, Sample{Labels: dropName(s.Labels), V: v})
				} else if ok {
					out = append(out, s)
				}
				continue
			}
			out = append(out, Sample{Labels: dropName(s.Labels), V: arith(b.Op, a, c)})
		}
		return Value{Vec: out}, nil
	}
	// vector with vector, one to one
	right := map[string]Sample{}
	for _, s := range r.Vec {
		k := signature(s.Labels, b)
		if _, dup := right[k]; dup {
			return Value{}, fmt.Errorf("found duplicate series on the right side of %q; many-to-one matching is not supported (use on/ignoring so each side has one series per match)", b.Op)
		}
		right[k] = s
	}
	seen := map[string]bool{}
	var out []Sample
	for _, s := range l.Vec {
		k := signature(s.Labels, b)
		o, ok := right[k]
		if !ok {
			continue
		}
		if seen[k] {
			return Value{}, fmt.Errorf("found duplicate series on the left side of %q; many-to-one matching is not supported", b.Op)
		}
		seen[k] = true
		if cmp {
			c := compare(b.Op, s.V, o.V)
			if b.Bool {
				v := 0.0
				if c {
					v = 1
				}
				out = append(out, Sample{Labels: resultLabels(s.Labels, b, true), V: v})
			} else if c {
				out = append(out, Sample{Labels: resultLabels(s.Labels, b, false), V: s.V})
			}
			continue
		}
		out = append(out, Sample{Labels: resultLabels(s.Labels, b, true), V: arith(b.Op, s.V, o.V)})
	}
	return Value{Vec: out}, nil
}

func setOp(b *Binary, l, r []Sample) Value {
	rs := map[string]bool{}
	for _, s := range r {
		rs[signature(s.Labels, b)] = true
	}
	var out []Sample
	switch b.Op {
	case "and":
		for _, s := range l {
			if rs[signature(s.Labels, b)] {
				out = append(out, s)
			}
		}
	case "unless":
		for _, s := range l {
			if !rs[signature(s.Labels, b)] {
				out = append(out, s)
			}
		}
	case "or":
		ls := map[string]bool{}
		for _, s := range l {
			ls[signature(s.Labels, b)] = true
			out = append(out, s)
		}
		for _, s := range r {
			if !ls[signature(s.Labels, b)] {
				out = append(out, s)
			}
		}
	}
	return Value{Vec: out}
}

func groupLabels(l Labels, a *Agg) Labels {
	o := Labels{}
	if !a.Grouping {
		return o
	}
	if a.Without {
		for k, v := range l {
			o[k] = v
		}
		delete(o, "__name__")
		for _, k := range a.By {
			delete(o, k)
		}
		return o
	}
	for _, k := range a.By {
		if v, ok := l[k]; ok {
			o[k] = v
		}
	}
	return o
}

func (e *evaluator) aggregate(a *Agg, t int64) (Value, error) {
	vec, err := e.vector(a.Arg, t)
	if err != nil {
		return Value{}, err
	}
	var param float64
	if a.Param != nil {
		if param, err = e.scalar(a.Param, t); err != nil {
			return Value{}, err
		}
	}
	type group struct {
		labels Labels
		items  []Sample
	}
	groups := map[string]*group{}
	var order []string
	for _, s := range vec {
		gl := groupLabels(s.Labels, a)
		k := labelString(gl)
		g := groups[k]
		if g == nil {
			g = &group{labels: gl}
			groups[k] = g
			order = append(order, k)
		}
		g.items = append(g.items, s)
	}
	sort.Strings(order)
	var out []Sample
	for _, k := range order {
		g := groups[k]
		switch a.Op {
		case "topk", "bottomk":
			items := append([]Sample(nil), g.items...)
			sort.SliceStable(items, func(i, j int) bool {
				if a.Op == "topk" {
					return items[i].V > items[j].V
				}
				return items[i].V < items[j].V
			})
			n := int(param)
			if n < 0 {
				n = 0
			}
			if n > len(items) {
				n = len(items)
			}
			out = append(out, items[:n]...)
			continue
		}
		vals := make([]float64, len(g.items))
		for i, s := range g.items {
			vals[i] = s.V
		}
		out = append(out, Sample{Labels: g.labels, V: reduce(a.Op, vals, param)})
	}
	return Value{Vec: out}, nil
}

// reduce applies an aggregation (or *_over_time) to a list of values.
func reduce(op string, v []float64, q float64) float64 {
	n := float64(len(v))
	switch op {
	case "sum":
		s := 0.0
		for _, x := range v {
			s += x
		}
		return s
	case "avg":
		return reduce("sum", v, 0) / n
	case "min":
		m := math.Inf(1)
		for _, x := range v {
			if x < m || math.IsNaN(x) {
				m = x
			}
		}
		return m
	case "max":
		m := math.Inf(-1)
		for _, x := range v {
			if x > m || math.IsNaN(x) {
				m = x
			}
		}
		return m
	case "count":
		return n
	case "group", "present":
		return 1
	case "stdvar", "stddev":
		mean := reduce("avg", v, 0)
		s := 0.0
		for _, x := range v {
			s += (x - mean) * (x - mean)
		}
		s /= n
		if op == "stddev" {
			return math.Sqrt(s)
		}
		return s
	case "quantile":
		return quantile(q, v)
	case "last":
		return v[len(v)-1]
	}
	return math.NaN()
}

func quantile(q float64, v []float64) float64 {
	if len(v) == 0 || math.IsNaN(q) {
		return math.NaN()
	}
	if q < 0 {
		return math.Inf(-1)
	}
	if q > 1 {
		return math.Inf(1)
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	rank := q * float64(len(s)-1)
	lo := math.Floor(rank)
	hi := math.Min(lo+1, float64(len(s)-1))
	w := rank - lo
	return s[int(lo)]*(1-w) + s[int(hi)]*w
}

var _ = strings.ToLower
