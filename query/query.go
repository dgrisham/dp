// Package query implements a small nushell/jq-inspired pipeline language: stages separated by
// '|' — `where <bool-expr>`, `get <path>`, `select <path>...`, and `length`. It operates on an
// already-decoded value (map[string]any/[]any/scalars).
//
// where's precedence (tightest to loosest): comparisons, `not`, `and`, `xor`, `or`; parens group.
package query

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Run returns the pipeline's result and, if its last stage was a select, the top-level field
// order it named (nil otherwise) — a display hint, since the result's own map keys have no order.
func Run(pipeline string, v any) (result any, columnOrder []string, err error) {
	stages, err := parse(pipeline)
	if err != nil {
		return nil, nil, err
	}
	result = v
	for _, s := range stages {
		result, err = s.apply(result)
		if err != nil {
			return nil, nil, err
		}
		if sel, ok := s.(selectStage); ok {
			columnOrder = sel.topLevelOrder()
		} else {
			columnOrder = nil
		}
	}
	return result, columnOrder, nil
}

type stage interface {
	apply(v any) (any, error)
}

func tokenize(s string) []string {
	var tokens []string
	var cur strings.Builder
	var inQuote byte
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote != 0:
			if c == inQuote {
				inQuote = 0
			} else {
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			inQuote = c
		case c == '|' || c == '(' || c == ')' || c == '[' || c == ']' || c == ',':
			flush()
			tokens = append(tokens, string(c))
		case c == ' ' || c == '\t':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return tokens
}

func parse(pipeline string) ([]stage, error) {
	tokens := tokenize(pipeline)
	var stages []stage
	var cur []string
	flush := func() error {
		if len(cur) == 0 {
			return fmt.Errorf("empty pipeline stage")
		}
		s, err := parseStage(cur)
		if err != nil {
			return err
		}
		stages = append(stages, s)
		cur = nil
		return nil
	}
	for _, t := range tokens {
		if t == "|" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		cur = append(cur, t)
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return stages, nil
}

func parseStage(tokens []string) (stage, error) {
	switch tokens[0] {
	case "where":
		p := &exprParser{tokens: tokens[1:]}
		node, err := p.parseOr()
		if err != nil {
			return nil, fmt.Errorf("where: %w", err)
		}
		if p.pos != len(p.tokens) {
			return nil, fmt.Errorf("where: unexpected token %q", p.tokens[p.pos])
		}
		return whereStage{cond: node}, nil
	case "get":
		if len(tokens) != 2 {
			return nil, fmt.Errorf(`get: expected "get <path>", got %q`, strings.Join(tokens, " "))
		}
		return getStage{path: splitPath(tokens[1])}, nil
	case "select":
		if len(tokens) < 2 {
			return nil, fmt.Errorf(`select: expected "select <path>...", got %q`, strings.Join(tokens, " "))
		}
		paths := make([][]string, len(tokens)-1)
		for i, t := range tokens[1:] {
			p := splitPath(t)
			if len(p) == 0 {
				return nil, fmt.Errorf(`select: "." (the item itself) isn't a field name — did you mean "get"?`)
			}
			paths[i] = p
		}
		return selectStage{paths: paths}, nil
	case "length":
		if len(tokens) != 1 {
			return nil, fmt.Errorf(`length: expected just "length", got %q`, strings.Join(tokens, " "))
		}
		return lengthStage{}, nil
	default:
		return nil, fmt.Errorf(`unknown pipeline stage %q (expected "where", "get", "select", or "length")`, tokens[0])
	}
}

func parseValue(tok string) any {
	switch tok {
	case "true":
		return true
	case "false":
		return false
	}
	if f, err := strconv.ParseFloat(tok, 64); err == nil {
		return f
	}
	return tok
}

// --- where's boolean expression grammar ---

type condNode interface {
	eval(item any) (bool, error)
}

type exprParser struct {
	tokens []string
	pos    int
}

func (p *exprParser) peek() string {
	if p.pos >= len(p.tokens) {
		return ""
	}
	return p.tokens[p.pos]
}

func (p *exprParser) next() string {
	t := p.peek()
	p.pos++
	return t
}

func (p *exprParser) parseOr() (condNode, error) {
	left, err := p.parseXor()
	if err != nil {
		return nil, err
	}
	for p.peek() == "or" {
		p.next()
		right, err := p.parseXor()
		if err != nil {
			return nil, err
		}
		left = orNode{left, right}
	}
	return left, nil
}

func (p *exprParser) parseXor() (condNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek() == "xor" {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = xorNode{left, right}
	}
	return left, nil
}

func (p *exprParser) parseAnd() (condNode, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.peek() == "and" {
		p.next()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = andNode{left, right}
	}
	return left, nil
}

func (p *exprParser) parseNot() (condNode, error) {
	if p.peek() == "not" {
		p.next()
		inner, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return notNode{inner}, nil
	}
	return p.parsePrimary()
}

func (p *exprParser) parsePrimary() (condNode, error) {
	if p.peek() == "(" {
		p.next()
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek() != ")" {
			return nil, fmt.Errorf(`expected ")", got %q`, p.peek())
		}
		p.next()
		return inner, nil
	}
	return p.parseComparison()
}

func (p *exprParser) parseComparison() (condNode, error) {
	pathTok := p.next()
	if pathTok == "" || pathTok == ")" {
		return nil, fmt.Errorf("expected a field path, got %q", pathTok)
	}
	opTok := p.next()
	o, ok := parseOp(opTok)
	if !ok {
		return nil, fmt.Errorf("unknown operator %q (expected ==, !=, <, >, <=, >=, =~, !~, in, not-in, starts-with, or ends-with)", opTok)
	}
	var value any
	if p.peek() == "[" {
		list, err := p.parseListLiteral()
		if err != nil {
			return nil, err
		}
		value = list
	} else {
		valTok := p.next()
		if valTok == "" {
			return nil, fmt.Errorf("expected a value after %q", opTok)
		}
		value = parseValue(valTok)
	}
	return newComparisonNode(splitPath(pathTok), o, value)
}

func (p *exprParser) parseListLiteral() ([]any, error) {
	p.next() // consume "["
	var items []any
	for {
		switch p.peek() {
		case "]":
			p.next()
			return items, nil
		case "":
			return nil, fmt.Errorf(`unterminated list literal, expected "]"`)
		case ",":
			p.next()
		default:
			items = append(items, parseValue(p.next()))
		}
	}
}

// xor has no short-circuit; and/or do.
type andNode struct{ left, right condNode }

func (n andNode) eval(item any) (bool, error) {
	l, err := n.left.eval(item)
	if err != nil || !l {
		return false, err
	}
	return n.right.eval(item)
}

type orNode struct{ left, right condNode }

func (n orNode) eval(item any) (bool, error) {
	l, err := n.left.eval(item)
	if err != nil || l {
		return l, err
	}
	return n.right.eval(item)
}

type xorNode struct{ left, right condNode }

func (n xorNode) eval(item any) (bool, error) {
	l, err := n.left.eval(item)
	if err != nil {
		return false, err
	}
	r, err := n.right.eval(item)
	if err != nil {
		return false, err
	}
	return l != r, nil
}

type notNode struct{ inner condNode }

func (n notNode) eval(item any) (bool, error) {
	v, err := n.inner.eval(item)
	return !v, err
}

type op int

const (
	opEq op = iota
	opNe
	opGt
	opLt
	opGe
	opLe
	opRegexMatch
	opRegexNotMatch
	opStartsWith
	opEndsWith
	opIn
	opNotIn
)

func parseOp(s string) (op, bool) {
	switch s {
	case "==":
		return opEq, true
	case "!=":
		return opNe, true
	case ">":
		return opGt, true
	case "<":
		return opLt, true
	case ">=":
		return opGe, true
	case "<=":
		return opLe, true
	case "=~":
		return opRegexMatch, true
	case "!~":
		return opRegexNotMatch, true
	case "starts-with":
		return opStartsWith, true
	case "ends-with":
		return opEndsWith, true
	case "in":
		return opIn, true
	case "not-in":
		return opNotIn, true
	}
	return 0, false
}

type comparisonNode struct {
	path  []string
	op    op
	value any
	regex *regexp.Regexp
}

// Regex is compiled at parse time so a bad pattern fails immediately, not mid-filter.
func newComparisonNode(path []string, o op, value any) (condNode, error) {
	n := comparisonNode{path: path, op: o, value: value}
	if o == opRegexMatch || o == opRegexNotMatch {
		pattern, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("=~/!~ requires a string pattern")
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regex %q: %w", pattern, err)
		}
		n.regex = re
	}
	return n, nil
}

func (n comparisonNode) eval(item any) (bool, error) {
	fieldVal, ok := getPath(item, n.path)
	if !ok {
		return false, nil // missing field never matches
	}
	switch n.op {
	case opRegexMatch:
		return n.regex.MatchString(fmt.Sprint(fieldVal)), nil
	case opRegexNotMatch:
		return !n.regex.MatchString(fmt.Sprint(fieldVal)), nil
	case opStartsWith:
		return strings.HasPrefix(fmt.Sprint(fieldVal), fmt.Sprint(n.value)), nil
	case opEndsWith:
		return strings.HasSuffix(fmt.Sprint(fieldVal), fmt.Sprint(n.value)), nil
	case opIn, opNotIn:
		matched := false
		if list, ok := n.value.([]any); ok {
			for _, item := range list {
				if equalValues(fieldVal, item) {
					matched = true
					break
				}
			}
		} else {
			matched = equalValues(fieldVal, n.value)
		}
		if n.op == opNotIn {
			return !matched, nil
		}
		return matched, nil
	case opEq:
		return equalValues(fieldVal, n.value), nil
	case opNe:
		return !equalValues(fieldVal, n.value), nil
	default: // opGt, opLt, opGe, opLe
		if ff, ok1 := toFloat(fieldVal); ok1 {
			if qf, ok2 := toFloat(n.value); ok2 {
				return compareOrdered(ff, qf, n.op), nil
			}
		}
		return compareOrdered(fmt.Sprint(fieldVal), fmt.Sprint(n.value), n.op), nil
	}
}

func compareOrdered[T float64 | string](a, b T, o op) bool {
	switch o {
	case opGt:
		return a > b
	case opLt:
		return a < b
	case opGe:
		return a >= b
	case opLe:
		return a <= b
	}
	return false
}

func equalValues(a, b any) bool {
	if af, ok1 := toFloat(a); ok1 {
		if bf, ok2 := toFloat(b); ok2 {
			return af == bf
		}
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// --- shared helpers ---

// splitPath: "." alone means the item itself (empty path), so where/get can target plain scalars.
func splitPath(tok string) []string {
	if tok == "." {
		return nil
	}
	return strings.Split(tok, ".")
}

// getPath walks a dot-path, descending into map fields by key and list elements by numeric index
// (negative counts from the end). When a list segment isn't a valid index, it's treated as a field
// name mapped across every element instead, and the rest of the path resolves against that — so
// both "0.id" (index then field) and "id.0" (field then index) work.
func getPath(v any, path []string) (any, bool) {
	result, ok, _ := getPathAt(v, path)
	return result, ok
}

// getPathAt is getPath, also reporting how many leading segments resolved before a failure, so a
// caller can report path[:consumed+1] — the exact segment that doesn't exist — instead of the
// whole path.
func getPathAt(v any, path []string) (result any, ok bool, consumed int) {
	if len(path) == 0 {
		return v, true, 0
	}
	seg, rest := path[0], path[1:]
	switch t := v.(type) {
	case map[string]any:
		next, ok := t[seg]
		if !ok {
			return nil, false, 0
		}
		val, ok2, c := getPathAt(next, rest)
		return val, ok2, c + 1
	case []any:
		if idx, numeric, inBounds := listIndex(seg, len(t)); numeric {
			if !inBounds {
				// A numeric segment out of range must fail, not fall through to field-name
				// mapping (which would trivially "succeed" over zero items).
				return nil, false, 0
			}
			val, ok2, c := getPathAt(t[idx], rest)
			return val, ok2, c + 1
		}
		mapped := make([]any, 0, len(t))
		for _, item := range t {
			val, ok := getPath(item, []string{seg})
			if !ok {
				return nil, false, 0
			}
			mapped = append(mapped, val)
		}
		val, ok2, c := getPathAt(mapped, rest)
		return val, ok2, c + 1
	default:
		return nil, false, 0
	}
}

// numeric reports whether seg looks like an index at all; inBounds is only meaningful if so.
func listIndex(seg string, length int) (idx int, numeric, inBounds bool) {
	n, err := strconv.Atoi(seg)
	if err != nil {
		return 0, false, false
	}
	if n < 0 {
		n += length
	}
	if n < 0 || n >= length {
		return 0, true, false
	}
	return n, true, true
}

func setNested(dst map[string]any, path []string, val any) {
	cur := dst
	for i, seg := range path {
		if i == len(path)-1 {
			cur[seg] = val
			return
		}
		next, ok := cur[seg].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[seg] = next
		}
		cur = next
	}
}

// --- stages ---

type whereStage struct {
	cond condNode
}

func (w whereStage) apply(v any) (any, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("where: input is not a list (pipe through a \"get\" of the list field first, e.g. \"get items | where ...\")")
	}
	out := []any{}
	for _, item := range list {
		matched, err := w.cond.eval(item)
		if err != nil {
			return nil, err
		}
		if matched {
			out = append(out, item)
		}
	}
	return out, nil
}

type getStage struct {
	path []string
}

func (g getStage) apply(v any) (any, error) {
	val, ok, consumed := getPathAt(v, g.path)
	if !ok {
		failedPath := g.path
		if consumed+1 < len(failedPath) {
			failedPath = failedPath[:consumed+1]
		}
		return nil, fmt.Errorf("get: field %q not found", strings.Join(failedPath, "."))
	}
	return val, nil
}

type selectStage struct {
	paths [][]string
}

func (s selectStage) topLevelOrder() []string {
	seen := map[string]bool{}
	var order []string
	for _, p := range s.paths {
		top := p[0]
		if !seen[top] {
			seen[top] = true
			order = append(order, top)
		}
	}
	return order
}

func (s selectStage) apply(v any) (any, error) {
	if list, ok := v.([]any); ok {
		out := make([]any, len(list))
		for i, item := range list {
			out[i] = s.selectFrom(item)
		}
		return out, nil
	}
	return s.selectFrom(v), nil
}

func (s selectStage) selectFrom(item any) map[string]any {
	result := map[string]any{}
	for _, path := range s.paths {
		if val, ok := getPath(item, path); ok {
			setNested(result, path, val)
		}
	}
	return result
}

// lengthStage errors on anything but a list (unlike jq, which also accepts a string/number/
// object/null) so a mistaken call is a loud error, not a plausible-looking wrong number.
type lengthStage struct{}

func (lengthStage) apply(v any) (any, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf(`length: input is not a list (pipe through a "get" of the list field first, e.g. "get items | length")`)
	}
	return float64(len(list)), nil
}
