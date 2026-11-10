package promql

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Labels is a label set. The metric name is the label __name__.
type Labels map[string]string

// Series is stored data for one label set, ordered by time (T in unix milliseconds).
type Series struct {
	Labels Labels
	T      []int64
	V      []float64
}

// Fetcher loads the series that match a selector with samples between from and to (inclusive).
type Fetcher func(ctx context.Context, vs *VectorSel, from, to time.Time) ([]Series, error)

type Options struct {
	Lookback time.Duration // how far back an instant selector looks for a sample (default 5m)
	MaxSteps int           // range queries only (default 11000)
}

// Sample is one element of an instant vector.
type Sample struct {
	Labels Labels
	V      float64
}

// Value is the result of an instant evaluation: a scalar, an instant vector or (for a bare range selector) a matrix.
type Value struct {
	Scalar bool
	F      float64
	Vec    []Sample
	Mat    []Series
	IsMat  bool
}

type evaluator struct {
	ctx  context.Context
	data map[*VectorSel][]Series
	lb   int64
}

func (o Options) lookback() time.Duration {
	if o.Lookback <= 0 {
		return 5 * time.Minute
	}
	return o.Lookback
}

// collect walks the expression and loads, once, all the data any step will need.
func (e *evaluator) load(x Expr, start, end time.Time, f Fetcher) error {
	var walk func(Expr) error
	need := func(vs *VectorSel, back time.Duration) error {
		from := start.Add(-vs.Offset - back)
		to := end.Add(-vs.Offset)
		d, err := f(e.ctx, vs, from, to)
		if err != nil {
			return err
		}
		e.data[vs] = d
		return nil
	}
	walk = func(x Expr) error {
		switch n := x.(type) {
		case *VectorSel:
			return need(n, time.Duration(e.lb)*time.Millisecond)
		case *MatrixSel:
			return need(n.VS, n.Range)
		case *Call:
			for _, a := range n.Args {
				if err := walk(a); err != nil {
					return err
				}
			}
		case *Agg:
			if n.Param != nil {
				if err := walk(n.Param); err != nil {
					return err
				}
			}
			return walk(n.Arg)
		case *Binary:
			if err := walk(n.L); err != nil {
				return err
			}
			return walk(n.R)
		case *Unary:
			return walk(n.X)
		}
		return nil
	}
	return walk(x)
}

// Instant evaluates an expression at one time.
func Instant(ctx context.Context, x Expr, at time.Time, f Fetcher, o Options) (Value, error) {
	e := &evaluator{ctx: ctx, data: map[*VectorSel][]Series{}, lb: o.lookback().Milliseconds()}
	if err := e.load(x, at, at, f); err != nil {
		return Value{}, err
	}
	return e.eval(x, at.UnixMilli())
}

// Range evaluates an expression at every step between start and end and returns one series per label set.
func Range(ctx context.Context, x Expr, start, end time.Time, step time.Duration, f Fetcher, o Options) ([]Series, error) {
	if step <= 0 {
		return nil, fmt.Errorf("step must be positive")
	}
	if end.Before(start) {
		return nil, fmt.Errorf("end is before start")
	}
	max := o.MaxSteps
	if max <= 0 {
		max = 11000
	}
	if int64(end.Sub(start)/step) > int64(max) {
		return nil, fmt.Errorf("exceeded maximum resolution of %d points per series; increase the step", max)
	}
	if _, ok := x.(*MatrixSel); ok {
		return nil, fmt.Errorf("a range vector cannot be graphed; wrap it in a function such as rate()")
	}
	e := &evaluator{ctx: ctx, data: map[*VectorSel][]Series{}, lb: o.lookback().Milliseconds()}
	if err := e.load(x, start, end, f); err != nil {
		return nil, err
	}
	out := map[string]*Series{}
	var order []string
	for t := start.UnixMilli(); t <= end.UnixMilli(); t += step.Milliseconds() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		v, err := e.eval(x, t)
		if err != nil {
			return nil, err
		}
		if v.Scalar {
			v = Value{Vec: []Sample{{Labels: Labels{}, V: v.F}}}
		}
		for _, s := range v.Vec {
			k := labelString(s.Labels)
			r := out[k]
			if r == nil {
				r = &Series{Labels: s.Labels}
				out[k] = r
				order = append(order, k)
			}
			r.T = append(r.T, t)
			r.V = append(r.V, s.V)
		}
	}
	sort.Strings(order)
	res := make([]Series, 0, len(order))
	for _, k := range order {
		res = append(res, *out[k])
	}
	return res, nil
}

func labelString(l Labels) string {
	ks := make([]string, 0, len(l))
	for k := range l {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	var b strings.Builder
	for _, k := range ks {
		b.WriteString(k)
		b.WriteByte(0)
		b.WriteString(l[k])
		b.WriteByte(1)
	}
	return b.String()
}

func dropName(l Labels) Labels {
	o := make(Labels, len(l))
	for k, v := range l {
		if k != "__name__" {
			o[k] = v
		}
	}
	return o
}

// MatchLabels reports whether a label set satisfies the matchers (used by the store adapter and the series API).
func MatchLabels(l Labels, ms []Matcher) bool {
	for _, m := range ms {
		v := l[m.Name]
		switch m.Op {
		case "=":
			if v != m.Value {
				return false
			}
		case "!=":
			if v == m.Value {
				return false
			}
		case "=~", "!~":
			re, err := regexp.Compile("^(?:" + m.Value + ")$")
			if err != nil {
				return false
			}
			if re.MatchString(v) != (m.Op == "=~") {
				return false
			}
		}
	}
	return true
}

func (e *evaluator) eval(x Expr, t int64) (Value, error) {
	switch n := x.(type) {
	case *NumberLit:
		return Value{Scalar: true, F: n.V}, nil
	case *StringLit:
		return Value{}, fmt.Errorf("a string is not a valid result here")
	case *VectorSel:
		at := t - n.Offset.Milliseconds()
		var out []Sample
		for _, s := range e.data[n] {
			i := sort.Search(len(s.T), func(i int) bool { return s.T[i] > at }) - 1
			if i >= 0 && s.T[i] > at-e.lb {
				out = append(out, Sample{Labels: s.Labels, V: s.V[i]})
			}
		}
		return Value{Vec: out}, nil
	case *MatrixSel:
		var out []Series
		for _, w := range e.window(n, t) {
			out = append(out, w)
		}
		return Value{IsMat: true, Mat: out}, nil
	}
	return e.eval2(x, t)
}

// window returns, for every series, the samples inside the range ending at t (minus the offset). Empty windows are left out.
func (e *evaluator) window(m *MatrixSel, t int64) []Series {
	end := t - m.VS.Offset.Milliseconds()
	start := end - m.Range.Milliseconds()
	var out []Series
	for _, s := range e.data[m.VS] {
		lo := sort.Search(len(s.T), func(i int) bool { return s.T[i] >= start })
		hi := sort.Search(len(s.T), func(i int) bool { return s.T[i] > end })
		if hi > lo {
			out = append(out, Series{Labels: s.Labels, T: s.T[lo:hi], V: s.V[lo:hi]})
		}
	}
	return out
}

func (e *evaluator) eval2(x Expr, t int64) (Value, error) {
	switch n := x.(type) {
	case *Unary:
		v, err := e.eval(n.X, t)
		if err != nil {
			return v, err
		}
		if v.IsMat {
			return v, fmt.Errorf("unary operator on a range vector")
		}
		if !n.Neg {
			return v, nil
		}
		if v.Scalar {
			return Value{Scalar: true, F: -v.F}, nil
		}
		out := make([]Sample, len(v.Vec))
		for i, s := range v.Vec {
			out[i] = Sample{Labels: dropName(s.Labels), V: -s.V}
		}
		return Value{Vec: out}, nil
	case *Binary:
		return e.binary(n, t)
	case *Agg:
		return e.aggregate(n, t)
	case *Call:
		return e.call(n, t)
	}
	return Value{}, fmt.Errorf("unsupported expression")
}

func (e *evaluator) vector(x Expr, t int64) ([]Sample, error) {
	v, err := e.eval(x, t)
	if err != nil {
		return nil, err
	}
	if v.Scalar || v.IsMat {
		return nil, fmt.Errorf("expected an instant vector")
	}
	return v.Vec, nil
}

func (e *evaluator) scalar(x Expr, t int64) (float64, error) {
	v, err := e.eval(x, t)
	if err != nil {
		return 0, err
	}
	if !v.Scalar {
		return 0, fmt.Errorf("expected a scalar")
	}
	return v.F, nil
}
