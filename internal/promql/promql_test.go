package promql

import (
	"context"
	"math"
	"testing"
	"time"
)

var t0 = time.Unix(1_700_000_000, 0)

func ser(l Labels, step time.Duration, vals ...float64) Series {
	s := Series{Labels: l}
	for i, v := range vals {
		s.T = append(s.T, t0.Add(time.Duration(i)*step).UnixMilli())
		s.V = append(s.V, v)
	}
	return s
}

// fetch serves a fixed set of series, filtered by name and matchers like the real adapter does.
func fetch(all ...Series) Fetcher {
	return func(_ context.Context, vs *VectorSel, _, _ time.Time) ([]Series, error) {
		var out []Series
		for _, s := range all {
			ms := append([]Matcher{}, vs.Matchers...)
			if vs.Name != "" {
				ms = append(ms, Matcher{"__name__", "=", vs.Name})
			}
			if MatchLabels(s.Labels, ms) {
				out = append(out, s)
			}
		}
		return out, nil
	}
}

func inst(t *testing.T, q string, at time.Time, f Fetcher) Value {
	t.Helper()
	x, err := Parse(q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	v, err := Instant(context.Background(), x, at, f, Options{})
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestScalarMath(t *testing.T) {
	for q, want := range map[string]float64{
		"1 + 2 * 3": 7, "(1 + 2) * 3": 9, "2 ^ 3 ^ 2": 512, "-2 ^ 2": -4, "10 % 4": 2, "7 / 2": 3.5,
		"1 < bool 2": 1, "abs(-3)": 3, "1e3 + 0x10": 1016,
	} {
		if v := inst(t, q, t0, fetch()); !v.Scalar || !near(v.F, want) {
			t.Errorf("%s = %+v, want %v", q, v, want)
		}
	}
}

func TestSelectorAndLookback(t *testing.T) {
	f := fetch(ser(Labels{"__name__": "up", "host": "a"}, 10*time.Second, 1, 1, 0), ser(Labels{"__name__": "up", "host": "b"}, 10*time.Second, 1, 1, 1))
	v := inst(t, `up{host="a"}`, t0.Add(25*time.Second), f)
	if len(v.Vec) != 1 || v.Vec[0].V != 0 {
		t.Fatalf("got %+v", v)
	}
	if v := inst(t, `up`, t0.Add(10*time.Minute), f); len(v.Vec) != 0 {
		t.Fatalf("a sample older than the lookback must not show: %+v", v)
	}
	if v := inst(t, `up{host=~"a|b"}`, t0.Add(25*time.Second), f); len(v.Vec) != 2 {
		t.Fatalf("regex: %+v", v)
	}
	if v := inst(t, `up{host!="a"}`, t0.Add(25*time.Second), f); len(v.Vec) != 1 {
		t.Fatalf("!=: %+v", v)
	}
	if v := inst(t, `up offset 10s`, t0.Add(25*time.Second), f); v.Vec[0].V != 1 {
		t.Fatalf("offset: %+v", v)
	}
}

func TestRateAndIncrease(t *testing.T) {
	// counter growing by 10 every 10 s, with a reset in the middle
	c := ser(Labels{"__name__": "req", "h": "a"}, 10*time.Second, 0, 10, 20, 30, 5, 15, 25)
	f := fetch(c)
	v := inst(t, `rate(req[1m])`, t0.Add(60*time.Second), f)
	if len(v.Vec) != 1 || v.Vec[0].V < 0.9 || v.Vec[0].V > 1.1 { // about 1 per second
		t.Fatalf("rate: %+v", v)
	}
	if _, ok := v.Vec[0].Labels["__name__"]; ok {
		t.Fatal("rate keeps the metric name")
	}
	i := inst(t, `increase(req[1m])`, t0.Add(60*time.Second), f)
	if i.Vec[0].V < 50 || i.Vec[0].V > 70 {
		t.Fatalf("increase: %+v", i)
	}
	if v := inst(t, `irate(req[1m])`, t0.Add(60*time.Second), f); !near(v.Vec[0].V, 1) {
		t.Fatalf("irate: %+v", v)
	}
	if v := inst(t, `rate(req[5s])`, t0.Add(60*time.Second), f); len(v.Vec) != 0 {
		t.Fatalf("one sample gives no rate: %+v", v)
	}
}

func TestOverTime(t *testing.T) {
	f := fetch(ser(Labels{"__name__": "m"}, 10*time.Second, 1, 2, 3, 4))
	at := t0.Add(30 * time.Second)
	for q, want := range map[string]float64{"avg_over_time(m[1m])": 2.5, "max_over_time(m[1m])": 4, "min_over_time(m[1m])": 1,
		"sum_over_time(m[1m])": 10, "count_over_time(m[1m])": 4, "last_over_time(m[1m])": 4, "quantile_over_time(0.5, m[1m])": 2.5} {
		if v := inst(t, q, at, f); len(v.Vec) != 1 || !near(v.Vec[0].V, want) {
			t.Errorf("%s = %+v, want %v", q, v, want)
		}
	}
}

func TestAggregates(t *testing.T) {
	f := fetch(
		ser(Labels{"__name__": "cpu", "host": "a", "dc": "x"}, time.Second, 10),
		ser(Labels{"__name__": "cpu", "host": "b", "dc": "x"}, time.Second, 30),
		ser(Labels{"__name__": "cpu", "host": "c", "dc": "y"}, time.Second, 50),
	)
	if v := inst(t, `sum(cpu)`, t0, f); len(v.Vec) != 1 || v.Vec[0].V != 90 {
		t.Fatalf("sum: %+v", v)
	}
	v := inst(t, `avg by (dc) (cpu)`, t0, f)
	if len(v.Vec) != 2 || v.Vec[0].Labels["dc"] != "x" || v.Vec[0].V != 20 {
		t.Fatalf("avg by: %+v", v)
	}
	if v := inst(t, `max(cpu) by (dc)`, t0, f); len(v.Vec) != 2 || v.Vec[1].V != 50 {
		t.Fatalf("max by after: %+v", v)
	}
	if v := inst(t, `count without (host) (cpu)`, t0, f); len(v.Vec) != 2 {
		t.Fatalf("without: %+v", v)
	}
	if v := inst(t, `topk(2, cpu)`, t0, f); len(v.Vec) != 2 || v.Vec[0].V != 50 || v.Vec[1].V != 30 {
		t.Fatalf("topk: %+v", v)
	}
	if v := inst(t, `bottomk(1, cpu)`, t0, f); len(v.Vec) != 1 || v.Vec[0].V != 10 {
		t.Fatalf("bottomk: %+v", v)
	}
	if v := inst(t, `stddev(cpu)`, t0, f); len(v.Vec) != 1 || v.Vec[0].V < 16 || v.Vec[0].V > 17 {
		t.Fatalf("stddev: %+v", v)
	}
}

func TestBinaryVectors(t *testing.T) {
	f := fetch(
		ser(Labels{"__name__": "used", "host": "a"}, time.Second, 25),
		ser(Labels{"__name__": "used", "host": "b"}, time.Second, 90),
		ser(Labels{"__name__": "total", "host": "a"}, time.Second, 100),
		ser(Labels{"__name__": "total", "host": "b"}, time.Second, 100),
		ser(Labels{"__name__": "limit"}, time.Second, 50),
	)
	v := inst(t, `used / total * 100`, t0, f)
	if len(v.Vec) != 2 || v.Vec[0].V != 25 || v.Vec[1].V != 90 {
		t.Fatalf("ratio: %+v", v)
	}
	if v := inst(t, `used / total * 100 > 80`, t0, f); len(v.Vec) != 1 || v.Vec[0].Labels["host"] != "b" {
		t.Fatalf("filter: %+v", v)
	}
}

func TestSetOperators(t *testing.T) {
	f := fetch(
		ser(Labels{"__name__": "a", "h": "1"}, time.Second, 1),
		ser(Labels{"__name__": "a", "h": "2"}, time.Second, 1),
		ser(Labels{"__name__": "b", "h": "2"}, time.Second, 1),
		ser(Labels{"__name__": "b", "h": "3"}, time.Second, 1),
	)
	for q, want := range map[string]int{"a and b": 1, "a unless b": 1, "a or b": 3} {
		if v := inst(t, q, t0, f); len(v.Vec) != want {
			t.Errorf("%s: %d series, want %d", q, len(v.Vec), want)
		}
	}
}

func TestManyToOneRefused(t *testing.T) {
	f := fetch(
		ser(Labels{"__name__": "used", "host": "a"}, time.Second, 25),
		ser(Labels{"__name__": "used", "host": "b"}, time.Second, 90),
		ser(Labels{"__name__": "limit", "x": "1"}, time.Second, 50),
		ser(Labels{"__name__": "limit", "x": "2"}, time.Second, 60),
	)
	x, _ := Parse(`used > on() limit`)
	if _, err := Instant(context.Background(), x, t0, f, Options{}); err == nil {
		t.Fatal("expected a many-to-one error")
	}
}

func TestRangeQuery(t *testing.T) {
	f := fetch(ser(Labels{"__name__": "g", "h": "a"}, 10*time.Second, 1, 2, 3, 4, 5, 6))
	x, _ := Parse(`g * 2`)
	m, err := Range(context.Background(), x, t0, t0.Add(50*time.Second), 10*time.Second, f, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || len(m[0].V) != 6 || m[0].V[5] != 12 {
		t.Fatalf("%+v", m)
	}
	if _, err := Range(context.Background(), x, t0, t0.Add(100*time.Hour), time.Second, f, Options{}); err == nil {
		t.Fatal("too many points must be refused")
	}
	mx, _ := Parse(`g[1m]`)
	if _, err := Range(context.Background(), mx, t0, t0.Add(time.Minute), time.Second, f, Options{}); err == nil {
		t.Fatal("range vector cannot be graphed")
	}
}

func TestInstantMatrix(t *testing.T) {
	f := fetch(ser(Labels{"__name__": "g"}, 10*time.Second, 1, 2, 3))
	v := inst(t, `g[30s]`, t0.Add(20*time.Second), f)
	if !v.IsMat || len(v.Mat) != 1 || len(v.Mat[0].V) != 3 {
		t.Fatalf("%+v", v)
	}
}

func TestFunctionsMisc(t *testing.T) {
	f := fetch(ser(Labels{"__name__": "g", "h": "a"}, time.Second, 5))
	if v := inst(t, `absent(nope{h="x"})`, t0, f); len(v.Vec) != 1 || v.Vec[0].V != 1 || v.Vec[0].Labels["h"] != "x" {
		t.Fatalf("absent: %+v", v)
	}
	if v := inst(t, `absent(g)`, t0, f); len(v.Vec) != 0 {
		t.Fatalf("absent present: %+v", v)
	}
	if v := inst(t, `scalar(g) + 1`, t0, f); !v.Scalar || v.F != 6 {
		t.Fatalf("scalar: %+v", v)
	}
	if v := inst(t, `vector(3)`, t0, f); len(v.Vec) != 1 || v.Vec[0].V != 3 {
		t.Fatalf("vector: %+v", v)
	}
	if v := inst(t, `time()`, t0, f); v.F != float64(t0.Unix()) {
		t.Fatalf("time: %+v", v)
	}
	if v := inst(t, `-g`, t0, f); v.Vec[0].V != -5 {
		t.Fatalf("neg: %+v", v)
	}
}

func TestParseErrors(t *testing.T) {
	for _, q := range []string{"", "sum(", "foo{", `foo{a=1}`, "rate(foo[5m]) by", "nosuchfunc(1)", "foo[5m:1m]", "foo @ 10", "a + on(x) group_left b",
		`foo{a=~"("}`, "1 +", "foo[abc]"} {
		if _, err := Parse(q); err == nil {
			t.Errorf("%q should not parse", q)
		}
	}
}

func TestDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{"5m": 5 * time.Minute, "1h30m": 90 * time.Minute, "2d": 48 * time.Hour, "500ms": 500 * time.Millisecond, "1w": 7 * 24 * time.Hour} {
		if d, err := ParseDuration(in); err != nil || d != want {
			t.Errorf("%s: %v %v", in, d, err)
		}
	}
	if _, err := ParseDuration("5"); err == nil {
		t.Error("5 has no unit")
	}
}
