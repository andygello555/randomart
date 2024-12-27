package nodes

import (
	"encoding/json"
	"fmt"
	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
	"github.com/pkg/errors"
	"io"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"time"
)

var (
	ErrReachedMaxDepth           = fmt.Errorf("reached max depth")
	ErrReachedMaxGenerationTries = fmt.Errorf("reached max generation tries")
	ErrRuleDoesNotExist          = fmt.Errorf("rule does not exist")
)

func pToP(p lexer.Position) pos {
	return pos{p.Filename, p.Line}
}

type production struct {
	*Production
	max    float64
	totals []float64
}

type GeneratorState struct {
	*generatorStateOptions
	seed  *rand.Rand
	rules map[string]*production
}

func (s *GeneratorState) Options() string {
	var b strings.Builder
	_ = json.NewEncoder(&b).Encode(s.generatorStateOptions)
	return b.String()
}

type Generator interface {
	fmt.Stringer
	Gen(state *GeneratorState, depth int) (Node, error)
}

type Alternate interface {
	Generator
	alt()
}

type Number struct {
	Pos   lexer.Position
	Value float64 `@Number`
}

func (f Number) alt() {}

func (f Number) String() string {
	return strconv.FormatFloat(f.Value, 'f', -1, 64)
}

func (f Number) Gen(state *GeneratorState, depth int) (Node, error) {
	return &value[float64]{pos: pToP(f.Pos), v: f.Value}, nil
}

type Boolean bool

func (b *Boolean) Capture(values []string) error {
	*b = values[0] == "true"
	return nil
}

type Bool struct {
	Pos   lexer.Position
	Value Boolean `@(True | False)`
}

func (f Bool) alt() {}

func (f Bool) String() string {
	return fmt.Sprintf("%t", f.Value)
}

func (f Bool) Gen(state *GeneratorState, depth int) (Node, error) {
	return &value[bool]{pos: pToP(f.Pos), v: bool(f.Value)}, nil
}

type Component struct {
	Pos       lexer.Position
	Component componentType `@Component`
}

func (f Component) alt() {}

func (f Component) String() string {
	return string(f.Component)
}

func (f Component) Gen(state *GeneratorState, depth int) (Node, error) {
	return &component{pos: pToP(f.Pos), ct: f.Component}, nil
}

type Triplet struct {
	Pos   lexer.Position
	One   Alternate `LCurly @@ Comma`
	Two   Alternate `       @@ Comma`
	Three Alternate `       @@ RCurly`
}

func (f Triplet) alt() {}

func (f Triplet) String() string {
	return fmt.Sprintf("{%s, %s, %s}", f.One, f.Two, f.Three)
}

func (f Triplet) Gen(state *GeneratorState, depth int) (Node, error) {
	one, err := f.One.Gen(state, depth)
	if err != nil {
		return nil, err
	}
	two, err := f.Two.Gen(state, depth)
	if err != nil {
		return nil, err
	}
	three, err := f.Three.Gen(state, depth)
	if err != nil {
		return nil, err
	}
	return &triple{
		pos:   pToP(f.Pos),
		one:   one,
		two:   two,
		three: three,
	}, nil
}

type Rule struct {
	Pos  lexer.Position
	Name string `@Ident`
}

func (f Rule) alt() {}

func (f Rule) String() string {
	return f.Name
}

func (f Rule) Gen(state *GeneratorState, depth int) (Node, error) {
	rule, ok := state.rules[f.Name]
	if !ok {
		return nil, errors.Wrapf(ErrRuleDoesNotExist, "%s referenced at %s", f.Name, f.Pos)
	}
	return rule.Gen(state, depth)
}

type Random struct {
	Pos    lexer.Position
	Random bool `@Random`
}

func (f Random) alt() {}

func (f Random) String() string {
	if f.Random {
		return "?"
	}
	return ""
}

func (f Random) Gen(state *GeneratorState, depth int) (Node, error) {
	return &value[float64]{pos: pToP(f.Pos), v: state.seed.Float64()*2 - 1}, nil
}

type Func struct {
	Pos      lexer.Position
	Operator opType    `@Operator LParen`
	Left     Alternate `@@ Comma`
	Right    Alternate `@@ RParen`
}

func (f Func) alt() {}

func (f Func) String() string {
	return fmt.Sprintf("%s(%s, %s)", f.Operator, f.Left, f.Right)
}

func (f Func) Gen(state *GeneratorState, depth int) (Node, error) {
	// TODO: Maybe we could do some type checking here? Or at least use some
	//       sort of heuristic to generate the correct type.
	left, err := f.Left.Gen(state, depth)
	if err != nil {
		return nil, err
	}
	right, err := f.Right.Gen(state, depth)
	if err != nil {
		return nil, err
	}
	return &op{
		pos:   pToP(f.Pos),
		t:     f.Operator,
		left:  left,
		right: right,
	}, nil
}

type IfThenElse struct {
	Pos  lexer.Position
	If   Alternate `If @@`
	Then Alternate `Then @@`
	Else Alternate `Else @@`
}

func (f IfThenElse) alt() {}

func (f IfThenElse) String() string {
	return fmt.Sprintf("if %s then %s else %s", f.If, f.Then, f.Else)
}

func (f IfThenElse) Gen(state *GeneratorState, depth int) (Node, error) {
	cond, err := f.If.Gen(state, depth)
	if err != nil {
		return nil, err
	}
	then, err := f.Then.Gen(state, depth)
	if err != nil {
		return nil, err
	}
	otherwise, err := f.Else.Gen(state, depth)
	if err != nil {
		return nil, err
	}
	return &ifThenElse{
		pos:       pToP(f.Pos),
		cond:      cond,
		then:      then,
		otherwise: otherwise,
	}, nil
}

type AlternateWithProb struct {
	Pos         lexer.Position
	Alternate   Alternate `@@`
	Probability float64   `Percent @Number`
}

func (a *AlternateWithProb) String() string {
	return fmt.Sprintf("%s %%%s", a.Alternate, strconv.FormatFloat(a.Probability, 'f', -1, 64))
}

type Production struct {
	Pos          lexer.Position
	Name         string               `@Ident ProductionEquals`
	Alternatives []*AlternateWithProb `@@ ( Pipe @@ )* Dot`
}

func (p *Production) String() string {
	var as []string
	for _, a := range p.Alternatives {
		as = append(as, a.String())
	}
	return fmt.Sprintf("%s ::= %s .", p.Name, strings.Join(as, " | "))
}

func (p *Production) Gen(state *GeneratorState, depth int) (node Node, err error) {
	if depth <= 0 {
		return nil, errors.Wrapf(ErrReachedMaxDepth, "%d depth", state.MaxDepth)
	}

	prod, ok := state.rules[p.Name]
	if !ok {
		panic(errors.Wrap(ErrRuleDoesNotExist, "in it's own method?"))
	}

	for try := 0; try < state.MaxGenerationTries; try++ {
		x := state.seed.Float64() * prod.max
		aNo, _ := slices.BinarySearch(prod.totals, x)
		aNo = min(aNo, len(p.Alternatives)-1)
		node, err = p.Alternatives[aNo].Alternate.Gen(state, depth-1)
		if err == nil {
			return node, nil
		} else if errors.Is(err, ErrRuleDoesNotExist) {
			return nil, err
		}
	}
	return nil, errors.Wrapf(ErrReachedMaxGenerationTries, "%d tries", state.MaxGenerationTries)
}

type Grammar struct {
	Pos         lexer.Position
	Productions []*Production `@@+`
}

func (g *Grammar) String() string {
	var b strings.Builder
	for _, production := range g.Productions {
		b.WriteString(production.String())
		b.WriteRune('\n')
	}
	return b.String()
}

type generatorStateOptions struct {
	Seed               uint64 `json:"seed"`
	MaxDepth           int    `json:"max_depth"`
	MaxGenerationTries int    `json:"max_generation_tries"`
}

func defaultGeneratorStateOptions() *generatorStateOptions {
	return &generatorStateOptions{
		Seed:               uint64(time.Now().Unix()),
		MaxDepth:           10,
		MaxGenerationTries: 100,
	}
}

type GeneratorOption func(o *generatorStateOptions) error

func WithSeeds(seed uint64) GeneratorOption {
	return func(o *generatorStateOptions) error {
		o.Seed = seed
		return nil
	}
}

func WithMaxDepth(depth int) GeneratorOption {
	return func(o *generatorStateOptions) error {
		o.MaxDepth = depth
		return nil
	}
}

func WithMaxGenerationTries(tries int) GeneratorOption {
	return func(o *generatorStateOptions) error {
		o.MaxGenerationTries = tries
		return nil
	}
}

func FromJSON(r io.Reader) GeneratorOption {
	return func(o *generatorStateOptions) error {
		return errors.Wrap(json.NewDecoder(r).Decode(o), "cannot decode generator options from JSON")
	}
}

func (g *Grammar) Gen(opts ...GeneratorOption) (Node, *GeneratorState, error) {
	options := defaultGeneratorStateOptions()
	for _, opt := range opts {
		if err := opt(options); err != nil {
			return nil, nil, err
		}
	}
	s := &GeneratorState{
		generatorStateOptions: options,
		seed:                  rand.New(rand.NewPCG(options.Seed, options.Seed+1)),
		rules:                 make(map[string]*production),
	}
	for _, p := range g.Productions {
		if firstProduction, ok := s.rules[p.Name]; ok {
			return nil, nil, fmt.Errorf(
				"production %s has been defined multiple times (at %s and %s)",
				p.Name, firstProduction.Pos, p.Pos,
			)
		}
		slices.SortFunc(p.Alternatives, func(a, b *AlternateWithProb) int {
			return int(a.Probability - b.Probability)
		})
		prod := production{
			Production: p,
			totals:     make([]float64, len(p.Alternatives)),
		}
		for i, a := range p.Alternatives {
			prod.max += a.Probability
			if prod.max > 1 {
				return nil, nil, fmt.Errorf("production %s's weights exceed 1 (%f)", p.Name, prod.max)
			}
			prod.totals[i] = prod.max
		}
		s.rules[p.Name] = &prod
	}
	node, err := g.Productions[0].Gen(s, options.MaxDepth)
	return node, s, err
}

var def = lexer.MustSimple([]lexer.SimpleRule{
	{"Component", componentTypePattern()},
	{"True", `true`},
	{"False", `false`},
	{"LParen", `\(`},
	{"RParen", `\)`},
	{"LCurly", `\{`},
	{"RCurly", `\}`},
	{"Comma", `,`},
	{"Random", `\?`},
	{"Percent", `%`},
	{"Pipe", `\|`},
	{"ProductionEquals", `\s::=\s`},
	{"Dot", `\.`},
	{"If", `if\s`},
	{"Then", `\sthen\s`},
	{"Else", `\selse\s`},
	{"Number", `[-+]?(\d*\.)?\d+`},
	{"Operator", opTypePattern()},
	{"Ident", `[A-Z]`},
	{"Whitespace", `\s+`},
})

var parser = participle.MustBuild[Grammar](
	participle.Lexer(def),
	participle.Elide("Whitespace"),
	participle.Union[Alternate](
		Triplet{},
		IfThenElse{},
		Number{},
		Bool{},
		Component{},
		Rule{},
		Random{},
		Func{},
	),
)

func Parse(r io.Reader, filename string) (*Grammar, error) {
	return parser.Parse(filename, r)
}
