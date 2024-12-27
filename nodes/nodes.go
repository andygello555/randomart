package nodes

import (
	"fmt"
	"image/color"
	"math"
	"regexp"
	"runtime"
	"strings"
)

type componentType string

const (
	xComponent componentType = "x"
	yComponent componentType = "y"
	fComponent componentType = "f"
	rComponent componentType = "r"
	gComponent componentType = "g"
	bComponent componentType = "b"
)

func componentTypes() []componentType {
	return []componentType{
		xComponent,
		yComponent,
		fComponent,
		rComponent,
		gComponent,
		bComponent,
	}
}

func componentTypePattern() string {
	var b strings.Builder
	b.WriteRune('[')
	cTypes := componentTypes()
	for _, c := range cTypes {
		b.WriteString(string(c))
	}
	b.WriteRune(']')
	return b.String()
}

var componentTypeRegex = regexp.MustCompile("^" + componentTypePattern() + "$")

func (ct componentType) Valid() bool {
	return componentTypeRegex.MatchString(string(ct))
}

type State struct {
	X, Y, F float64
	R, G, B float64
}

func (s *State) component(c componentType) float64 {
	switch c {
	case xComponent:
		return s.X
	case yComponent:
		return s.Y
	case fComponent:
		return s.F
	case rComponent:
		return s.R
	case gComponent:
		return s.G
	case bComponent:
		return s.B
	}
	panic(fmt.Errorf("%s is not a valid component for %T", c, s))
}

func S(x, y, width, height, frame, frames int, src color.Color) State {
	r, g, b, _ := src.RGBA()
	return State{
		X: float64(x)/float64(width-1)*2 - 1,
		Y: float64(y)/float64(height-1)*2 - 1,
		F: float64(frame)/float64(frames-1)*2 - 1,
		R: float64(r)/0xFFFF*2 - 1,
		G: float64(g)/0xFFFF*2 - 1,
		B: float64(b)/0xFFFF*2 - 1,
	}
}

type notA string

const (
	number  notA = "number"
	boolean notA = "boolean"
	root    notA = "triple"
)

type ValidationError struct {
	Node
	is notA
}

func (v *ValidationError) Error() string {
	return fmt.Sprintf("%s at %s:%d is not %s", v.Node, v.File(), v.Line(), v.is)
}

type Pos interface {
	File() string
	Line() int
}

type Node interface {
	fmt.Stringer
	Pos
	Eval(state State) (Node, error)
}

type pos struct {
	file string
	line int
}

func (p pos) File() string {
	return p.file
}

func (p pos) Line() int {
	return p.line
}

func p() pos {
	_, file, line, ok := runtime.Caller(2)
	if !ok {
		panic("cannot recover caller information")
	}
	return pos{
		file: file,
		line: line,
	}
}

func isNumber(n Node) (float64, error) {
	v, ok := n.(*value[float64])
	if ok {
		return v.v, nil
	}
	return 0, &ValidationError{
		Node: n,
		is:   number,
	}
}

func isBoolean(n Node) (bool, error) {
	v, ok := n.(*value[bool])
	if ok {
		return v.v, nil
	}
	return false, &ValidationError{
		Node: n,
		is:   boolean,
	}
}

type supportedValueTypes interface {
	float64 | bool
}

type value[T supportedValueTypes] struct {
	pos
	v T
}

func (v *value[T]) String() string {
	return fmt.Sprintf("%v", v.v)
}

func (v *value[T]) Eval(state State) (Node, error) {
	return v, nil
}

func Val[T supportedValueTypes](v T) Node { return &value[T]{pos: p(), v: v} }

type component struct {
	pos
	ct componentType
}

func (c *component) String() string {
	return string(c.ct)
}

func (c *component) Eval(state State) (Node, error) {
	return &value[float64]{pos: c.pos, v: state.component(c.ct)}, nil
}

type opType string

const (
	add opType = "add"
	sub opType = "sub"
	mul opType = "mul"
	div opType = "div"
	mod opType = "mod"
	gt  opType = "gt"
	ge  opType = "ge"
	lt  opType = "lt"
	le  opType = "le"
)

func opTypes() []opType {
	return []opType{
		add,
		sub,
		mul,
		div,
		mod,
		gt,
		ge,
		lt,
		le,
	}
}

func opTypePattern() string {
	var b strings.Builder
	values := opTypes()
	for i, o := range values {
		b.WriteString(string(o))
		if i < len(values)-1 {
			b.WriteString("|")
		}
	}
	return b.String()
}

type op struct {
	pos
	t     opType
	left  Node
	right Node
}

func (o *op) String() string {
	return fmt.Sprintf("%s(%s, %s)", o.t, o.left, o.right)
}

func (o *op) Eval(state State) (Node, error) {
	left, err := o.left.Eval(state)
	if err != nil {
		return nil, err
	}
	right, err := o.right.Eval(state)
	if err != nil {
		return nil, err
	}
	leftN, err := isNumber(left)
	if err != nil {
		return nil, err
	}
	rightN, err := isNumber(right)
	if err != nil {
		return nil, err
	}
	var result any
	switch o.t {
	case add:
		result = leftN + rightN
	case sub:
		result = leftN - rightN
	case mul:
		result = leftN * rightN
	case div:
		result = leftN / rightN
	case mod:
		result = math.Mod(leftN, rightN)
	case gt:
		result = leftN > rightN
	case ge:
		result = leftN >= rightN
	case lt:
		result = leftN < rightN
	case le:
		result = leftN <= rightN
	}
	switch v := result.(type) {
	case float64:
		return &value[float64]{pos: o.pos, v: v}, nil
	case bool:
		return &value[bool]{pos: o.pos, v: v}, nil
	}
	return nil, fmt.Errorf("%q operator is not handled", o.t)
}

func Add(left, right Node) Node { return &op{pos: p(), t: add, left: left, right: right} }
func Sub(left, right Node) Node { return &op{pos: p(), t: sub, left: left, right: right} }
func Mul(left, right Node) Node { return &op{pos: p(), t: mul, left: left, right: right} }
func Div(left, right Node) Node { return &op{pos: p(), t: div, left: left, right: right} }
func Mod(left, right Node) Node { return &op{pos: p(), t: mod, left: left, right: right} }
func Gt(left, right Node) Node  { return &op{pos: p(), t: gt, left: left, right: right} }
func Ge(left, right Node) Node  { return &op{pos: p(), t: ge, left: left, right: right} }
func Lt(left, right Node) Node  { return &op{pos: p(), t: lt, left: left, right: right} }
func Le(left, right Node) Node  { return &op{pos: p(), t: le, left: left, right: right} }

type triple struct {
	pos
	one   Node
	two   Node
	three Node
}

func (t *triple) String() string {
	return fmt.Sprintf("(%s, %s, %s)", t.one, t.two, t.three)
}

func (t *triple) Eval(state State) (Node, error) {
	one, err := t.one.Eval(state)
	if err != nil {
		return nil, err
	}
	two, err := t.two.Eval(state)
	if err != nil {
		return nil, err
	}
	three, err := t.three.Eval(state)
	if err != nil {
		return nil, err
	}
	return &triple{
		pos:   t.pos,
		one:   one,
		two:   two,
		three: three,
	}, nil
}

func Triple(one, two, three Node) Node { return &triple{pos: p(), one: one, two: two, three: three} }

func IsRoot(n Node) (float64, float64, float64, error) {
	t, ok := n.(*triple)
	if !ok {
		return 0, 0, 0, &ValidationError{
			Node: n,
			is:   root,
		}
	}
	one, err := isNumber(t.one)
	if err != nil {
		return 0, 0, 0, err
	}
	two, err := isNumber(t.two)
	if err != nil {
		return 0, 0, 0, err
	}
	three, err := isNumber(t.three)
	if err != nil {
		return 0, 0, 0, err
	}
	return one, two, three, nil
}

type ifThenElse struct {
	pos
	cond      Node
	then      Node
	otherwise Node
}

func (i *ifThenElse) String() string {
	return fmt.Sprintf("if %s then %s else %s", i.cond, i.then, i.otherwise)
}

func (i *ifThenElse) Eval(state State) (Node, error) {
	cond, err := i.cond.Eval(state)
	if err != nil {
		return nil, err
	}
	c, err := isBoolean(cond)
	if err != nil {
		return nil, err
	}
	if c {
		return i.then.Eval(state)
	}
	return i.otherwise.Eval(state)
}

func If(cond, then, otherwise Node) Node {
	return &ifThenElse{pos: p(), cond: cond, then: then, otherwise: otherwise}
}
