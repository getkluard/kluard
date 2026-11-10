// Package promql implements a subset of PromQL, enough for Grafana dashboards and typical alert expressions:
// selectors, range vectors, rate/increase/delta and the *_over_time functions, aggregations with by/without,
// arithmetic, comparison and set operators with on/ignoring, and a handful of scalar functions.
// It has no dependencies. Subqueries, @ modifiers, group_left/right and native histograms are not supported.
package promql

import "time"

// Expr is a node of a parsed expression.
type Expr interface{}

type NumberLit struct{ V float64 }
type StringLit struct{ V string }

// MatchOp is one of = != =~ !~.
type Matcher struct {
	Name, Op, Value string
}

type VectorSel struct {
	Name     string // metric name, "" when only matchers were given
	Matchers []Matcher
	Offset   time.Duration
}

type MatrixSel struct {
	VS    *VectorSel
	Range time.Duration
}

type Call struct {
	Func string
	Args []Expr
}

type Agg struct {
	Op       string
	Param    Expr // topk, bottomk, quantile
	Arg      Expr
	By       []string
	Without  bool
	Grouping bool // by or without was given
}

type Binary struct {
	Op       string
	L, R     Expr
	Bool     bool
	On       bool // on(...) given
	Ignoring bool // ignoring(...) given
	Labels   []string
}

type Unary struct {
	Neg bool
	X   Expr
}
