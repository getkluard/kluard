package promql

import (
	"fmt"
	"math"
	"sort"
)

// funcs lists the supported functions (the parser uses it to reject unknown names).
var funcs = map[string]struct{}{}

func init() {
	for _, n := range []string{"rate", "irate", "increase", "delta", "idelta", "resets", "changes",
		"avg_over_time", "sum_over_time", "min_over_time", "max_over_time", "count_over_time", "last_over_time",
		"stddev_over_time", "stdvar_over_time", "quantile_over_time", "present_over_time",
		"abs", "ceil", "floor", "round", "sqrt", "exp", "ln", "log2", "log10", "sgn",
		"clamp", "clamp_min", "clamp_max", "time", "vector", "scalar", "absent", "sort", "sort_desc"} {
		funcs[n] = struct{}{}
	}
}

// extrapolated follows Prometheus: the increase over the window, extended to the window edges when the samples
// nearly cover it, and for counters corrected for resets.
func extrapolated(s Series, start, end int64, counter, rate bool) (float64, bool) {
	n := len(s.V)
	if n < 2 {
		return 0, false
	}
	res := s.V[n-1] - s.V[0]
	if counter {
		for i := 1; i < n; i++ {
			if s.V[i] < s.V[i-1] {
				res += s.V[i-1]
			}
		}
	}
	toStart := float64(s.T[0]-start) / 1000
	toEnd := float64(end-s.T[n-1]) / 1000
	sampled := float64(s.T[n-1]-s.T[0]) / 1000
	avg := sampled / float64(n-1)
	if counter && res > 0 && s.V[0] >= 0 {
		if z := sampled * (s.V[0] / res); z < toStart {
			toStart = z
		}
	}
	thr := avg * 1.1
	ext := sampled
	if toStart < thr {
		ext += toStart
	} else {
		ext += avg / 2
	}
	if toEnd < thr {
		ext += toEnd
	} else {
		ext += avg / 2
	}
	factor := ext / sampled
	if rate {
		factor /= float64(end-start) / 1000
	}
	return res * factor, true
}

func (e *evaluator) call(c *Call, t int64) (Value, error) {
	need := func(n int) error {
		if len(c.Args) != n {
			return fmt.Errorf("%s expects %d argument(s), got %d", c.Func, n, len(c.Args))
		}
		return nil
	}
	switch c.Func {
	case "time":
		if err := need(0); err != nil {
			return Value{}, err
		}
		return Value{Scalar: true, F: float64(t) / 1000}, nil
	case "vector":
		if err := need(1); err != nil {
			return Value{}, err
		}
		f, err := e.scalar(c.Args[0], t)
		return Value{Vec: []Sample{{Labels: Labels{}, V: f}}}, err
	case "scalar":
		if err := need(1); err != nil {
			return Value{}, err
		}
		v, err := e.vector(c.Args[0], t)
		if err != nil {
			return Value{}, err
		}
		if len(v) != 1 {
			return Value{Scalar: true, F: math.NaN()}, nil
		}
		return Value{Scalar: true, F: v[0].V}, nil
	case "absent":
		if err := need(1); err != nil {
			return Value{}, err
		}
		v, err := e.vector(c.Args[0], t)
		if err != nil || len(v) > 0 {
			return Value{}, err
		}
		l := Labels{}
		if vs, ok := c.Args[0].(*VectorSel); ok {
			for _, m := range vs.Matchers {
				if m.Op == "=" {
					l[m.Name] = m.Value
				}
			}
			if vs.Name != "" {
				l["__name__"] = vs.Name
			}
			delete(l, "__name__")
		}
		return Value{Vec: []Sample{{Labels: l, V: 1}}}, nil
	case "sort", "sort_desc":
		if err := need(1); err != nil {
			return Value{}, err
		}
		v, err := e.vector(c.Args[0], t)
		if err != nil {
			return Value{}, err
		}
		v = append([]Sample(nil), v...)
		sort.SliceStable(v, func(i, j int) bool {
			if c.Func == "sort" {
				return v[i].V < v[j].V
			}
			return v[i].V > v[j].V
		})
		return Value{Vec: v}, nil
	case "clamp", "clamp_min", "clamp_max", "round":
		want := map[string]int{"clamp": 3, "clamp_min": 2, "clamp_max": 2}[c.Func]
		if c.Func == "round" {
			if len(c.Args) < 1 || len(c.Args) > 2 {
				return Value{}, fmt.Errorf("round expects 1 or 2 arguments")
			}
		} else if err := need(want); err != nil {
			return Value{}, err
		}
		v, err := e.vector(c.Args[0], t)
		if err != nil {
			return Value{}, err
		}
		args := make([]float64, 0, 2)
		for _, a := range c.Args[1:] {
			f, err := e.scalar(a, t)
			if err != nil {
				return Value{}, err
			}
			args = append(args, f)
		}
		out := make([]Sample, len(v))
		for i, s := range v {
			x := s.V
			switch c.Func {
			case "clamp":
				x = math.Max(args[0], math.Min(args[1], x))
			case "clamp_min":
				x = math.Max(args[0], x)
			case "clamp_max":
				x = math.Min(args[0], x)
			case "round":
				n := 1.0
				if len(args) > 0 {
					n = args[0]
				}
				x = math.Floor(x/n+0.5) * n
			}
			out[i] = Sample{Labels: dropName(s.Labels), V: x}
		}
		return Value{Vec: out}, nil
	case "abs", "ceil", "floor", "sqrt", "exp", "ln", "log2", "log10", "sgn":
		if err := need(1); err != nil {
			return Value{}, err
		}
		fn := map[string]func(float64) float64{"abs": math.Abs, "ceil": math.Ceil, "floor": math.Floor, "sqrt": math.Sqrt,
			"exp": math.Exp, "ln": math.Log, "log2": math.Log2, "log10": math.Log10,
			"sgn": func(x float64) float64 {
				switch {
				case x > 0:
					return 1
				case x < 0:
					return -1
				}
				return x
			}}[c.Func]
		v, err := e.eval(c.Args[0], t)
		if err != nil {
			return v, err
		}
		if v.Scalar {
			return Value{Scalar: true, F: fn(v.F)}, nil
		}
		if v.IsMat {
			return Value{}, fmt.Errorf("%s needs an instant vector", c.Func)
		}
		out := make([]Sample, len(v.Vec))
		for i, s := range v.Vec {
			out[i] = Sample{Labels: dropName(s.Labels), V: fn(s.V)}
		}
		return Value{Vec: out}, nil
	}
	return e.rangeFunc(c, t)
}

func (e *evaluator) rangeFunc(c *Call, t int64) (Value, error) {
	var q float64
	args := c.Args
	if c.Func == "quantile_over_time" {
		if len(args) != 2 {
			return Value{}, fmt.Errorf("quantile_over_time expects 2 arguments")
		}
		var err error
		if q, err = e.scalar(args[0], t); err != nil {
			return Value{}, err
		}
		args = args[1:]
	}
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%s expects a range vector argument", c.Func)
	}
	m, ok := args[0].(*MatrixSel)
	if !ok {
		return Value{}, fmt.Errorf("%s needs a range vector such as metric[5m]", c.Func)
	}
	end := t - m.VS.Offset.Milliseconds()
	start := end - m.Range.Milliseconds()
	var out []Sample
	for _, s := range e.window(m, t) {
		var v float64
		okv := true
		n := len(s.V)
		switch c.Func {
		case "rate":
			v, okv = extrapolated(s, start, end, true, true)
		case "increase":
			v, okv = extrapolated(s, start, end, true, false)
		case "delta":
			v, okv = extrapolated(s, start, end, false, false)
		case "irate", "idelta":
			if n < 2 {
				okv = false
				break
			}
			d := s.V[n-1] - s.V[n-2]
			if c.Func == "irate" {
				if d < 0 {
					d = s.V[n-1]
				}
				v = d / (float64(s.T[n-1]-s.T[n-2]) / 1000)
			} else {
				v = d
			}
		case "resets", "changes":
			for i := 1; i < n; i++ {
				if (c.Func == "resets" && s.V[i] < s.V[i-1]) || (c.Func == "changes" && s.V[i] != s.V[i-1]) {
					v++
				}
			}
		case "avg_over_time":
			v = reduce("avg", s.V, 0)
		case "sum_over_time":
			v = reduce("sum", s.V, 0)
		case "min_over_time":
			v = reduce("min", s.V, 0)
		case "max_over_time":
			v = reduce("max", s.V, 0)
		case "count_over_time":
			v = float64(n)
		case "last_over_time":
			v = s.V[n-1]
		case "present_over_time":
			v = 1
		case "stddev_over_time":
			v = reduce("stddev", s.V, 0)
		case "stdvar_over_time":
			v = reduce("stdvar", s.V, 0)
		case "quantile_over_time":
			v = quantile(q, s.V)
		default:
			return Value{}, fmt.Errorf("unsupported function %s", c.Func)
		}
		if okv {
			out = append(out, Sample{Labels: dropName(s.Labels), V: v})
		}
	}
	return Value{Vec: out}, nil
}
