package verify

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/project"
)

// A call's build scope is read from the conditions around it: the
// preprocessor blocks it is in (#if, #ifdef, #ifndef, #elif, #elseif, #else),
// and in Unity's C# the ifs on Debug.isDebugBuild that guard it. Each
// condition becomes a boolean expression over the symbols it names, and the
// expressions are tried with every value of those symbols.

// buildScope says which builds run a call.
type buildScope int

const (
	// everyBuild includes release builds.
	everyBuild buildScope = iota
	debugBuilds
	// noBuild: the call is never compiled for the platform asked about.
	noBuild
	// unknownScope: a condition around the call could not be read.
	unknownScope
)

// isDebugBuild stands for Unity's Debug.isDebugBuild in an expression.
const isDebugBuild = "Debug.isDebugBuild"

// debugSymbols are defined in debug or development builds only: Xcode's and
// Unity's DEBUG, Unity's DEVELOPMENT_BUILD and UNITY_EDITOR.
var debugSymbols = map[string]bool{"DEBUG": true, "DEVELOPMENT_BUILD": true, "UNITY_EDITOR": true, isDebugBuild: true}

// releaseMarker is C's NDEBUG. Xcode does not define it in release builds
// by default, so a block on its absence may still be in them.
const releaseMarker = "NDEBUG"

// unityPlatformSymbols are Unity's platform symbols: the mobile platforms'
// own, and every other platform's, which a mobile player build never has.
var unityPlatformSymbols = map[project.Platform][]string{
	project.IOS:     {"UNITY_IOS", "UNITY_IPHONE"},
	project.Android: {"UNITY_ANDROID"},
	"": {"UNITY_STANDALONE", "UNITY_STANDALONE_OSX", "UNITY_STANDALONE_WIN", "UNITY_STANDALONE_LINUX", "UNITY_SERVER",
		"UNITY_WEBGL", "UNITY_TVOS", "UNITY_VISIONOS", "UNITY_WSA", "UNITY_WSA_10_0", "UNITY_PS4", "UNITY_PS5", "UNITY_XBOXONE",
		"UNITY_GAMECORE", "UNITY_SWITCH", "UNITY_EMBEDDED_LINUX", "UNITY_QNX"},
}

// unitySymbols are Unity's platform symbols as a player build for the
// platform has them: its own defined, every other platform's not.
func unitySymbols(platform project.Platform) map[string]bool {
	fixed := map[string]bool{}
	for p, names := range unityPlatformSymbols {
		for _, name := range names {
			fixed[name] = p == platform
		}
	}
	return fixed
}

// maxSymbols bounds the symbols an expression may name to be tried.
const maxSymbols = 16

// scopeOf reads which builds run a call. platform, when given, also fixes
// Unity's platform symbols, so a block for another platform never counts.
func scopeOf(c call, platform project.Platform) buildScope {
	ext := path.Ext(c.File)
	code := project.BlankStrings(c.Text, ext)
	conds := directiveConds(code, c.Line)
	if ext == ".cs" && c.At > 0 && c.At <= len(code) {
		conds = append(conds, runtimeConds(code, c.At)...)
	}
	var fixed map[string]bool
	if ext == ".cs" && platform != "" {
		fixed = unitySymbols(platform)
	}
	return classify(and(conds...), fixed)
}

// unityPlatforms says which of the mobile platforms compile a line of a C#
// file, from the #if blocks around it: a line under #if UNITY_IOS is iOS's
// alone, one under its #else every other platform's, Android included.
func unityPlatforms(code string, line int) (android, ios bool) {
	conds := and(directiveConds(code, line)...)
	return classify(conds, unitySymbols(project.Android)) != noBuild, classify(conds, unitySymbols(project.IOS)) != noBuild
}

// debugOnly reports whether a call is compiled into, or run in, debug builds
// only.
func debugOnly(c call) bool { return scopeOf(c, "") == debugBuilds }

// expr is a boolean expression over symbols.
type expr struct {
	op   byte // 'v' symbol, 'c' constant, '!', '&', '|'
	name string
	val  bool
	args []*expr
}

func symbol(name string) *expr { return &expr{op: 'v', name: name} }
func constant(v bool) *expr    { return &expr{op: 'c', val: v} }
func not(e *expr) *expr        { return &expr{op: '!', args: []*expr{e}} }

// opaque stands for a condition that could not be read: a symbol of its own
// that may be true or false in any build.
func opaque(text string) *expr { return symbol("?" + text) }

func and(es ...*expr) *expr {
	if len(es) == 1 {
		return es[0]
	}
	if len(es) == 0 {
		return constant(true)
	}
	return &expr{op: '&', args: es}
}

func (e *expr) eval(v map[string]bool) bool {
	switch e.op {
	case 'v':
		return v[e.name]
	case 'c':
		return e.val
	case '!':
		return !e.args[0].eval(v)
	case '&':
		for _, a := range e.args {
			if !a.eval(v) {
				return false
			}
		}
		return true
	default:
		for _, a := range e.args {
			if a.eval(v) {
				return true
			}
		}
		return false
	}
}

func (e *expr) symbols(seen map[string]bool, out []string) []string {
	if e.op == 'v' && !seen[e.name] {
		seen[e.name] = true
		out = append(out, e.name)
	}
	for _, a := range e.args {
		out = a.symbols(seen, out)
	}
	return out
}

// classify tries the expression with every value of its symbols. It is
// debug-only when every way it holds has a debug symbol defined, and in
// release builds when it holds with none defined (and NDEBUG defined, where
// it is named) whatever its unreadable parts mean.
func classify(e *expr, fixed map[string]bool) buildScope {
	var free, unread []string
	hasMarker := false
	for _, s := range e.symbols(map[string]bool{}, nil) {
		if _, ok := fixed[s]; ok {
			continue
		}
		if strings.HasPrefix(s, "?") {
			unread = append(unread, s)
			continue
		}
		hasMarker = hasMarker || s == releaseMarker
		free = append(free, s)
	}
	if len(free)+len(unread) > maxSymbols {
		return unknownScope
	}
	v := map[string]bool{}
	for s, val := range fixed {
		v[s] = val
	}
	holds, onlyDebug, inRelease := false, true, false
	for f := 0; f < 1<<len(free); f++ {
		debug := false
		for i, s := range free {
			v[s] = f&(1<<i) != 0
			debug = debug || (v[s] && debugSymbols[s])
		}
		always := true
		for u := 0; u < 1<<len(unread); u++ {
			for i, s := range unread {
				v[s] = u&(1<<i) != 0
			}
			if e.eval(v) {
				holds = true
				onlyDebug = onlyDebug && debug
			} else {
				always = false
			}
		}
		if always && !debug && (!hasMarker || v[releaseMarker]) {
			inRelease = true
		}
	}
	switch {
	case !holds:
		return noBuild
	case onlyDebug:
		return debugBuilds
	case inRelease:
		return everyBuild
	}
	return unknownScope
}

// directiveConds returns the conditions of the preprocessor blocks that
// hold the given line, an #elif or #else taking the negation of the
// branches before it.
func directiveConds(code string, line int) []*expr {
	type block struct{ before, cond *expr }
	var open []*block
	lines := strings.Split(code, "\n")
	for i := 0; i < line-1 && i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(t, "#") {
			continue
		}
		for strings.HasSuffix(t, `\`) && i+1 < line-1 {
			i++
			t = strings.TrimSuffix(t, `\`) + " " + strings.TrimSpace(lines[i])
		}
		t = strings.TrimSpace(t[1:])
		word := t
		if end := strings.IndexFunc(t, func(r rune) bool { return r < 'a' || r > 'z' }); end >= 0 {
			word = t[:end]
		}
		rest := strings.TrimSpace(t[len(word):])
		var top *block
		if len(open) > 0 {
			top = open[len(open)-1]
		}
		switch {
		case word == "if":
			open = append(open, &block{before: constant(false), cond: parseCondition(rest, false)})
		case word == "ifdef":
			open = append(open, &block{before: constant(false), cond: parseCondition("defined "+rest, false)})
		case word == "ifndef":
			open = append(open, &block{before: constant(false), cond: parseCondition("!defined "+rest, false)})
		case (word == "elif" || word == "elseif") && top != nil:
			top.before = &expr{op: '|', args: []*expr{top.before, top.cond}}
			top.cond = parseCondition(rest, false)
		case word == "else" && top != nil:
			top.before = &expr{op: '|', args: []*expr{top.before, top.cond}}
			top.cond = constant(true)
		case word == "endif" && top != nil:
			open = open[:len(open)-1]
		}
	}
	var conds []*expr
	for _, b := range open {
		conds = append(conds, and(not(b.before), b.cond))
	}
	return conds
}

var conditionToken = regexp.MustCompile(`^\s*(?:&&|\|\||==|!=|<=|>=|[!()<>+\-*/%&|^~?:,]|[A-Za-z_]\w*(?:\s*\.\s*[A-Za-z_]\w*)*|\d\w*|"[^"\n]*"|'[^'\n]*')`)

// parseCondition reads a preprocessor condition, or with runtime a C#
// one. What it cannot read becomes opaque; in a C# condition only a part
// that names Debug.isDebugBuild matters, and any other is a value that can
// be anything in any build.
func parseCondition(text string, runtime bool) *expr {
	var toks []string
	for rest := text; strings.TrimSpace(rest) != ""; {
		m := conditionToken.FindStringSubmatch(rest)
		if m == nil || strings.ContainsAny(m[0], `"'`) && !runtime {
			return opaque(text)
		}
		toks = append(toks, strings.Join(strings.Fields(m[0]), ""))
		rest = rest[len(m[0]):]
	}
	p := &condParser{toks: toks, runtime: runtime}
	e := p.or()
	if p.failed || p.i != len(toks) {
		return opaque(text)
	}
	return e
}

type condParser struct {
	toks    []string
	i       int
	runtime bool
	failed  bool
}

func (p *condParser) peek() string {
	if p.i < len(p.toks) {
		return p.toks[p.i]
	}
	return ""
}

func (p *condParser) next() string {
	t := p.peek()
	p.i++
	return t
}

func (p *condParser) or() *expr {
	args := []*expr{p.and()}
	for p.peek() == "||" {
		p.next()
		args = append(args, p.and())
	}
	if len(args) == 1 {
		return args[0]
	}
	return &expr{op: '|', args: args}
}

func (p *condParser) and() *expr {
	args := []*expr{p.compare()}
	for p.peek() == "&&" {
		p.next()
		args = append(args, p.compare())
	}
	return and(args...)
}

// compare reads a comparison with true or false; any other operator makes
// the whole comparison unreadable.
func (p *condParser) compare() *expr {
	start := p.i
	e := p.unary()
	switch op := p.peek(); {
	case (op == "==" || op == "!=") && p.i+1 < len(p.toks) && (p.toks[p.i+1] == "true" || p.toks[p.i+1] == "false"):
		p.i += 2
		if (op == "==") != (p.toks[p.i-1] == "true") {
			return not(e)
		}
		return e
	case op != "" && op != "&&" && op != "||" && op != ")":
		depth := 0
		for ; p.i < len(p.toks); p.i++ {
			t := p.toks[p.i]
			if depth == 0 && (t == "&&" || t == "||" || t == ")") {
				break
			}
			switch t {
			case "(":
				depth++
			case ")":
				depth--
			}
		}
		return p.unreadable(strings.Join(p.toks[start:p.i], " "))
	}
	return e
}

func (p *condParser) unreadable(text string) *expr {
	if p.runtime && !mentionsDebugBuild.MatchString(text) {
		return symbol(text)
	}
	return opaque(text)
}

func (p *condParser) unary() *expr {
	if p.peek() == "!" {
		p.next()
		return not(p.unary())
	}
	return p.primary()
}

func (p *condParser) primary() *expr {
	start := p.i
	switch t := p.next(); {
	case t == "(":
		e := p.or()
		if p.next() != ")" {
			p.failed = true
		}
		return e
	case t == "defined" && !p.runtime:
		paren := p.peek() == "("
		if paren {
			p.next()
		}
		name := p.next()
		if !conditionName.MatchString(name) || (paren && p.next() != ")") {
			p.failed = true
		}
		return symbol(name)
	case t == "true" || t == "false":
		return constant(t == "true")
	case t != "" && (t[0] == '"' || t[0] == '\''):
		return p.unreadable(t)
	case t != "" && t[0] >= '0' && t[0] <= '9':
		n, err := strconv.ParseInt(strings.TrimRight(t, "uUlL"), 0, 64)
		if err != nil {
			p.failed = true
		}
		return constant(n != 0)
	case conditionName.MatchString(t):
		if p.peek() == "(" {
			// A function-like test, os(iOS) or a method call.
			depth := 0
			for ; p.i < len(p.toks); p.i++ {
				if p.toks[p.i] == "(" {
					depth++
				} else if p.toks[p.i] == ")" {
					if depth--; depth == 0 {
						p.i++
						break
					}
				}
			}
			if !p.runtime {
				return symbol(strings.Join(p.toks[start:p.i], ""))
			}
			return p.unreadable(strings.Join(p.toks[start:p.i], ""))
		}
		if p.runtime && (t == isDebugBuild || t == "UnityEngine."+isDebugBuild) {
			return symbol(isDebugBuild)
		}
		if p.runtime && mentionsDebugBuild.MatchString(t) {
			return opaque(t)
		}
		return symbol(t)
	}
	p.failed = true
	return constant(false)
}

var (
	conditionName      = regexp.MustCompile(`^[A-Za-z_][\w.]*$`)
	mentionsDebugBuild = regexp.MustCompile(`\bisDebugBuild\b`)
)

// runtimeConds returns the conditions on Debug.isDebugBuild that guard the
// C# code at pos: the ifs and elses around it, and early returns before it
// in the blocks that hold it. code has its comments and strings blanked.
func runtimeConds(code string, pos int) []*expr {
	var conds []*expr
	stmt := statementStart(code, pos)
	conds = append(conds, headerConds(code, stmt, pos)...)
	end := stmt
	depth := 0
	for i := stmt - 1; i >= 0; i-- {
		switch code[i] {
		case '}':
			depth++
		case '{':
			if depth > 0 {
				depth--
				continue
			}
			conds = append(conds, earlyReturns(code, i+1, end)...)
			conds = append(conds, blockConds(code, i)...)
			end = i
		}
	}
	return conds
}

// statementStart returns where the statement holding pos begins: just
// after the ;, { or } before it, outside parentheses.
func statementStart(code string, pos int) int {
	depth := 0
	for i := pos - 1; i >= 0; i-- {
		switch code[i] {
		case ')':
			depth++
		case '(':
			if depth > 0 {
				depth--
			}
		case ';', '{', '}':
			if depth == 0 {
				return i + 1
			}
		}
	}
	return 0
}

// headerConds reads the if and else headers between a statement's start and
// pos, as in `if (Debug.isDebugBuild) instance.DebugLogs();`.
func headerConds(code string, from, to int) []*expr {
	var conds []*expr
	for i := from; i < to; {
		for i < to && isSpace(code[i]) {
			i++
		}
		switch word := wordAt(code, i); word {
		case "if":
			open := skipSpace(code, i+2)
			if open >= to || code[open] != '(' {
				return conds
			}
			closing := matchForward(code, open, to)
			if closing < 0 {
				return conds
			}
			conds = appendCond(conds, code[open+1:closing], false)
			i = closing + 1
		case "else":
			conds = append(conds, priorNegations(code, i, 0)...)
			i += 4
		default:
			return conds
		}
	}
	return conds
}

// blockConds returns the conditions of the if or else whose block opens at
// open.
func blockConds(code string, open int) []*expr {
	if start, cond, ok := ifHeaderBefore(code, open); ok {
		conds := appendCond(nil, cond, false)
		if e, ok := elseBefore(code, start); ok {
			conds = append(conds, priorNegations(code, e, 0)...)
		}
		return conds
	}
	if e, ok := elseBefore(code, open); ok {
		return priorNegations(code, e, 0)
	}
	return nil
}

// priorNegations returns the negated conditions of the branches before the
// else at pos.
func priorNegations(code string, pos, depth int) []*expr {
	if depth > 32 {
		return nil
	}
	j := pos - 1
	for j >= 0 && isSpace(code[j]) {
		j--
	}
	if j < 0 {
		return nil
	}
	var start int
	var cond string
	switch code[j] {
	case '}':
		open := matchBack(code, j, '{', '}')
		var ok bool
		if start, cond, ok = ifHeaderBefore(code, open); !ok {
			return unreadElse(code, pos)
		}
	case ';':
		start = skipSpace(code, statementStart(code, j))
		if wordAt(code, start) == "else" {
			start = skipSpace(code, start+4)
		}
		open := skipSpace(code, start+2)
		if wordAt(code, start) != "if" || open >= j || code[open] != '(' {
			return unreadElse(code, pos)
		}
		closing := matchForward(code, open, j)
		if closing < 0 {
			return unreadElse(code, pos)
		}
		cond = code[open+1 : closing]
	default:
		return unreadElse(code, pos)
	}
	conds := appendCond(nil, cond, true)
	if e, ok := elseBefore(code, start); ok {
		conds = append(conds, priorNegations(code, e, depth+1)...)
	}
	return conds
}

// unreadElse is an else whose if could not be found: unreadable when the
// text before it tests Debug.isDebugBuild.
func unreadElse(code string, pos int) []*expr {
	from := max(0, pos-500)
	if mentionsDebugBuild.MatchString(code[from:pos]) {
		return []*expr{opaque("else")}
	}
	return nil
}

// appendCond adds a C# condition when it tests Debug.isDebugBuild, negated
// when asked.
func appendCond(conds []*expr, text string, negate bool) []*expr {
	if !mentionsDebugBuild.MatchString(text) {
		return conds
	}
	e := parseCondition(text, true)
	if negate {
		e = not(e)
	}
	return append(conds, e)
}

// earlyReturns returns the negated conditions of `if (…) return;`
// statements directly in the block between from and to.
func earlyReturns(code string, from, to int) []*expr {
	var conds []*expr
	depth := 0
	for i := from; i < to; i++ {
		switch code[i] {
		case '{':
			depth++
			continue
		case '}':
			depth--
			continue
		}
		if depth != 0 || wordAt(code, i) != "if" || (i > 0 && isWord(code[i-1])) {
			continue
		}
		open := skipSpace(code, i+2)
		if open >= to || code[open] != '(' {
			continue
		}
		closing := matchForward(code, open, to)
		if closing < 0 {
			continue
		}
		body := skipSpace(code, closing+1)
		returns := false
		switch {
		case wordAt(code, body) == "return":
			semi := strings.IndexByte(code[body:to], ';')
			returns = semi >= 0
		case body < to && code[body] == '{':
			end := matchForward(code, body, to)
			inner := ""
			if end >= 0 {
				inner = strings.TrimSpace(code[body+1 : end])
			}
			returns = wordAt(inner, 0) == "return" && strings.Count(inner, ";") == 1 && strings.HasSuffix(inner, ";")
		}
		if returns {
			conds = appendCond(conds, code[open+1:closing], true)
		}
		i = closing
	}
	return conds
}

// ifHeaderBefore reads `if (cond)` just before pos, returning where the if
// starts and its condition.
func ifHeaderBefore(code string, pos int) (int, string, bool) {
	j := pos - 1
	for j >= 0 && isSpace(code[j]) {
		j--
	}
	if j < 0 || code[j] != ')' {
		return 0, "", false
	}
	open := matchBack(code, j, '(', ')')
	if open < 0 {
		return 0, "", false
	}
	k := open - 1
	for k >= 0 && isSpace(code[k]) {
		k--
	}
	if k < 1 || code[k-1:k+1] != "if" || (k >= 2 && isWord(code[k-2])) {
		return 0, "", false
	}
	return k - 1, code[open+1 : j], true
}

// elseBefore finds the word else just before pos.
func elseBefore(code string, pos int) (int, bool) {
	j := pos - 1
	for j >= 0 && isSpace(code[j]) {
		j--
	}
	if j < 3 || code[j-3:j+1] != "else" || (j >= 4 && isWord(code[j-4])) {
		return 0, false
	}
	return j - 3, true
}

// matchBack returns the opening bracket that matches the closing one at j.
func matchBack(code string, j int, opening, closing byte) int {
	depth := 0
	for i := j; i >= 0; i-- {
		switch code[i] {
		case closing:
			depth++
		case opening:
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}

// matchForward returns the bracket that closes the one at i, before limit.
func matchForward(code string, i, limit int) int {
	opening := code[i]
	closing := map[byte]byte{'(': ')', '{': '}'}[opening]
	depth := 0
	for j := i; j < limit && j < len(code); j++ {
		switch code[j] {
		case opening:
			depth++
		case closing:
			if depth--; depth == 0 {
				return j
			}
		}
	}
	return -1
}

// wordAt returns the identifier that starts at i.
func wordAt(code string, i int) string {
	if i >= len(code) {
		return ""
	}
	j := i
	for j < len(code) && isWord(code[j]) {
		j++
	}
	return code[i:j]
}

func skipSpace(code string, i int) int {
	for i < len(code) && isSpace(code[i]) {
		i++
	}
	return i
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

func isWord(b byte) bool {
	return b == '_' || '0' <= b && b <= '9' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z'
}
