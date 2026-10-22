package project

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// sourceFile is one of the app's source files, read once. What is worked
// out from it is kept, so that each wrapper and each lookup does not read
// it again.
type sourceFile struct {
	rel, ext string
	// text has its comments blanked; code has its strings' contents
	// blanked too. Offsets are the same in both.
	text, code string
	// index is the file's place in the scan, which orders what is found.
	index int
	// sdks are the analytics SDKs the file uses.
	sdks []*analyticsSDK

	newlines  []int
	callIndex map[string][]callSite
	sendIndex map[string][]sendSite
	scopeList []scope
	scoped    bool
	decls     []declaration
	declared  bool
	importsOf []string
	imported  bool
	pkgName   *string
	names     map[string]map[string]bool
}

// line returns the line an offset is on.
func (f *sourceFile) line(offset int) int {
	if f.newlines == nil {
		f.newlines = []int{}
		for i := 0; i < len(f.text); i++ {
			if f.text[i] == '\n' {
				f.newlines = append(f.newlines, i)
			}
		}
	}
	return sort.SearchInts(f.newlines, offset) + 1
}

// callSite is a call by name: where the name starts, just past its opening
// parenthesis, and whether it is made on something (a.name()).
type callSite struct {
	at, open int
	member   bool
}

// calls indexes the file's calls by name, once.
func (f *sourceFile) calls() map[string][]callSite {
	if f.callIndex != nil {
		return f.callIndex
	}
	f.callIndex = map[string][]callSite{}
	code := f.code
	for i := 0; i < len(code); {
		c := code[i]
		if !wordByte(c) {
			i++
			continue
		}
		j := i + 1
		for j < len(code) && wordByte(code[j]) {
			j++
		}
		if c >= '0' && c <= '9' {
			i = j
			continue
		}
		k := j
		for k < len(code) && space(code[k]) {
			k++
		}
		if k < len(code) && code[k] == '(' {
			p := i - 1
			for p >= 0 && space(code[p]) {
				p--
			}
			name := code[i:j]
			f.callIndex[name] = append(f.callIndex[name], callSite{at: i, open: k + 1, member: p >= 0 && code[p] == '.'})
		}
		i = j
	}
	return f.callIndex
}

// sendSite is an Objective-C message: its receiver, where its first
// selector part starts, and where its argument does.
type sendSite struct {
	recvStart, recvEnd, at, arg int
}

var objcSend = regexp.MustCompile(`\[\s*([^\[\]\s]+|\[[^\[\]]*\])\s+(\w+)\s*:`)

// sends indexes the file's Objective-C messages by their first selector
// part, once.
func (f *sourceFile) sends() map[string][]sendSite {
	if f.sendIndex != nil {
		return f.sendIndex
	}
	f.sendIndex = map[string][]sendSite{}
	for _, m := range objcSend.FindAllStringSubmatchIndex(f.code, -1) {
		name := f.code[m[4]:m[5]]
		f.sendIndex[name] = append(f.sendIndex[name], sendSite{recvStart: m[2], recvEnd: m[3], at: m[4], arg: m[1]})
	}
	return f.sendIndex
}

func wordByte(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

func space(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// scope is the body of a class, object or other container: its name, and
// its braces.
type scope struct {
	name        string
	open, close int
}

var (
	containers = regexp.MustCompile(`\b(?:(?:enum|data|sealed|abstract|open|inner|annotation|value|static|final|partial|public|private|internal|export|default)\s+)*(?:class|object|struct|enum|extension|interface|mixin|protocol|namespace)\s+(\w+)|@implementation\s+(\w+)|\b(?:const|let|var)\s+(\w+)\s*=\s*\{`)
	notAName   = map[string]bool{"func": true, "var": true, "let": true, "fun": true, "val": true, "class": true, "static": true, "override": true, "extends": true}
	afterName  = regexp.MustCompile(`^\s*(?:class|object|struct|enum|interface|fun|func|val|var|let|const|import|@)\b`)
)

// scopes lists the file's containers, once, in the order they open.
func (f *sourceFile) scopes() []scope {
	if f.scoped {
		return f.scopeList
	}
	f.scoped = true
	code := f.code
	for _, m := range containers.FindAllStringSubmatchIndex(code, -1) {
		var name string
		group := 0
		for g := 2; g < len(m); g += 2 {
			if m[g] >= 0 {
				name, group = code[m[g]:m[g+1]], g
			}
		}
		if name == "" || notAName[name] {
			continue
		}
		switch group {
		case 4:
			end := strings.Index(code[m[1]:], "@end")
			if end < 0 {
				end = len(code) - m[1]
			}
			f.scopeList = append(f.scopeList, scope{name, m[1], m[1] + end})
		case 6:
			open := m[1] - 1
			f.scopeList = append(f.scopeList, scope{name, open, closingBrace(code, open)})
		default:
			if open := bodyOf(code, m[1]); open >= 0 {
				f.scopeList = append(f.scopeList, scope{name, open, closingBrace(code, open)})
			}
		}
	}
	return f.scopeList
}

// bodyOf finds the brace that opens a container's body, from just past its
// name: after its parameters, supertypes and constraints, and before
// anything that shows it has none.
func bodyOf(code string, i int) int {
	depth := 0
	for j := i; j < len(code) && j < i+2000; j++ {
		switch c := code[j]; {
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case depth > 0:
		case c == '{':
			return j
		case c == ';' || c == '}' || c == '=':
			return -1
		case c == '\n' && afterName.MatchString(code[j+1:min(len(code), j+40)]):
			return -1
		}
	}
	return -1
}

// closingBrace returns the offset of the brace that closes the one at open,
// or the end of the code.
func closingBrace(code string, open int) int {
	depth := 0
	for j := open; j < len(code); j++ {
		switch code[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return len(code)
}

// innermost returns the container an offset is in, or nil.
func (f *sourceFile) innermost(at int) *scope {
	var in *scope
	for i, s := range f.scopes() {
		if s.open > at {
			break
		}
		if at < s.close {
			in = &f.scopes()[i]
		}
	}
	return in
}

// enclosing returns the containers an offset is in, the innermost first.
func (f *sourceFile) enclosing(at int) []scope {
	var out []scope
	for _, s := range f.scopes() {
		if s.open > at {
			break
		}
		if at < s.close {
			out = append([]scope{s}, out...)
		}
	}
	return out
}

func (f *sourceFile) containerOf(at int) string {
	if s := f.innermost(at); s != nil {
		return s.name
	}
	return ""
}

var importLine = regexp.MustCompile(`(?m)^[ \t]*(?:import|export|part)\s+(?:static\s+)?(?:['"]([^'"]+)['"]|([\w.]+(?:\.\*)?))`)

// imports lists what the file imports: a Kotlin or Java name, or a Dart
// path.
func (f *sourceFile) imports() []string {
	if f.imported {
		return f.importsOf
	}
	f.imported = true
	for _, m := range importLine.FindAllStringSubmatch(f.text, -1) {
		f.importsOf = append(f.importsOf, m[1]+m[2])
	}
	return f.importsOf
}

var packageLine = regexp.MustCompile(`(?m)^[ \t]*package\s+([\w.]+)`)

// pkg is the file's Kotlin or Java package.
func (f *sourceFile) pkg() string {
	if f.pkgName == nil {
		name := ""
		if m := packageLine.FindStringSubmatch(f.code); m != nil {
			name = m[1]
		}
		f.pkgName = &name
	}
	return *f.pkgName
}

// family groups the extensions that call each other's functions.
func family(ext string) string {
	switch ext {
	case ".kt", ".java":
		return "jvm"
	case ".m", ".mm":
		return "objc"
	case ".swift", ".dart", ".cs":
		return ext
	}
	if isJS(ext) {
		return "js"
	}
	return ext
}

var (
	declarationKeywords = regexp.MustCompile(`^(?:return|new|throw|else|case|await|yield|in|is|as|if|for|while|switch|catch|using|lock|foreach|synchronized|when|typeof|delete|var|val|let|const)$`)
	declarations        = map[string]*regexp.Regexp{
		"jvm":    regexp.MustCompile(`\bfun\s+(?:<[^>]*>\s*)?(?:[\w.]+\.)?(\w+)\s*\(|(?m)^[ \t]*(?:(?:public|private|protected|static|final|synchronized|abstract)\s+)*([\w.<>\[\]?,]+)\s+(\w+)\s*\(`),
		".swift": regexp.MustCompile(`\bfunc\s+(\w+)\s*(?:<[^>]*>)?\s*\(`),
		"js":     regexp.MustCompile(`\bfunction\s*\*?\s*(\w+)\s*\(|\b(\w+)\s*[=:]\s*(?:async\s+)?(?:function\s*)?\(|(?m)^[ \t]*(?:(?:static|async|public|private|protected|override)\s+)*(\w+)\s*\([^()]*\)\s*(?::\s*[\w<>\[\]|. ]+)?\s*\{`),
		// Dart and C#: a return type, then the name.
		"typed": regexp.MustCompile(`(?m)^[ \t]*(?:(?:public|private|protected|internal|static|final|override|virtual|async|abstract|sealed|external)\s+)*([\w.<>\[\]?,]+)\s+(\w+)\s*\(`),
		"objc":  regexp.MustCompile(`(?m)^[ \t]*[-+]\s*\([^)]*\)\s*(\w+)\s*:\s*\([^)]*\)\s*(\w+)`),
	}
)

// declaration is a function declared in the app's code.
type declaration struct {
	name   string
	nameAt int
	// objcParam is the first parameter of an Objective-C method.
	objcParam string
}

// declarationsOf lists the functions a file declares, in order, once.
func (f *sourceFile) declarations() []declaration {
	if f.declared {
		return f.decls
	}
	f.declared = true
	fam := family(f.ext)
	re := declarations[fam]
	if fam == ".dart" || fam == ".cs" {
		re = declarations["typed"]
	}
	if re == nil {
		return nil
	}
	for _, m := range re.FindAllStringSubmatchIndex(f.code, -1) {
		if fam == "objc" {
			f.decls = append(f.decls, declaration{name: f.code[m[2]:m[3]], nameAt: m[2], objcParam: f.code[m[4]:m[5]]})
			continue
		}
		// The last group that matched is the name; the one before it, when
		// it matched, the return type, which return f( has too.
		last := -1
		for g := 2; g < len(m); g += 2 {
			if m[g] >= 0 {
				last = g
			}
		}
		if last < 0 {
			continue
		}
		name := f.code[m[last]:m[last+1]]
		if declarationKeywords.MatchString(name) || (last > 2 && m[last-2] >= 0 && declarationKeywords.MatchString(f.code[m[last-2]:m[last-1]])) {
			continue
		}
		f.decls = append(f.decls, declaration{name: name, nameAt: m[last]})
	}
	sort.Slice(f.decls, func(i, j int) bool { return f.decls[i].nameAt < f.decls[j].nameAt })
	return f.decls
}

// parameter is one of a function's parameters: the name it has inside the
// function, and the label a call gives it, if any.
type parameter struct {
	name, label string
	position    int // among the positional parameters, or -1 for a named one
}

var parameterNoise = regexp.MustCompile(`@\w+|\[[^\]]*\]|\brequired\b|\bfinal\b|\bvararg\b|\bval\b|\bvar\b|\bparams\b|\bconst\b|\bthis\.|[?*&!]`)

// parameters reads a declaration's parameter list.
func parameters(f *sourceFile, d declaration) []parameter {
	if d.objcParam != "" {
		return []parameter{{name: d.objcParam, label: d.name, position: 0}}
	}
	open := strings.IndexByte(f.text[d.nameAt:], '(')
	if open < 0 {
		return nil
	}
	var out []parameter
	position := 0
	for _, raw := range splitParams(f.text[d.nameAt+open+1:]) {
		// Dart's named parameters are in braces, its optional positional
		// ones in brackets.
		if f.ext == ".dart" && (strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[")) {
			named := raw[0] == '{'
			for _, inner := range splitParams(raw[1:]) {
				p, ok := parameterOf(inner, f.ext)
				if !ok {
					continue
				}
				if named {
					p.label, p.position = p.name, -1
				} else {
					p.position = position
					position++
				}
				out = append(out, p)
			}
			continue
		}
		p, ok := parameterOf(raw, f.ext)
		if !ok {
			continue
		}
		p.position = position
		position++
		out = append(out, p)
	}
	return out
}

// parameterOf reads one parameter: name: Type in Kotlin, Swift and
// TypeScript, Type name elsewhere.
func parameterOf(raw, ext string) (parameter, bool) {
	if i := strings.IndexByte(raw, '='); i >= 0 {
		raw = raw[:i]
	}
	raw = parameterNoise.ReplaceAllString(raw, " ")
	var p parameter
	words := strings.Fields(raw)
	if i := strings.IndexByte(raw, ':'); i >= 0 && (ext == ".kt" || ext == ".swift" || isJS(ext)) {
		words = strings.Fields(raw[:i])
		if len(words) > 1 && ext == ".swift" && words[0] != "_" {
			p.label = words[0]
		} else if len(words) == 1 && ext == ".swift" {
			p.label = words[0]
		}
	}
	if len(words) == 0 {
		return p, false
	}
	p.name = words[len(words)-1]
	return p, identifier.MatchString(p.name)
}

// splitParams splits a parameter list, from just past its opening
// parenthesis, at the commas outside brackets and type arguments.
func splitParams(text string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(text) && i < 2000; i++ {
		switch text[i] {
		case '"', '\'', '`':
			i = endOfString(text, i) - 1
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}', '>':
			if depth == 0 {
				if part := strings.TrimSpace(text[start:i]); part != "" {
					out = append(out, part)
				}
				return out
			}
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(text[start:i]))
				start = i + 1
			}
		}
	}
	return out
}

// wrapper is a function of the app's own that passes one of its
// parameters on to an SDK as an event's name or the user's ID.
type wrapper struct {
	sdk    *analyticsSDK
	kind   analyticsKind
	name   string
	family string
	param  int // position among the positional parameters, or -1
	label  string
	file   string
	line   int
	declAt int
	// container is the class or object the wrapper is declared in, or ""
	// for a function of its own.
	container string
	inside    sighting
	callers   int
	// home is the file the wrapper is declared in.
	home *sourceFile
}

// wrapperOf works out whether the code at a call that gives an SDK a name,
// a bare identifier, is a parameter of the function the call is in: then
// that function is the app's way of sending to the SDK, and its callers
// give the names.
func (s *analyticsScan) wrapperOf(f *sourceFile, sdk *analyticsSDK, at int, ident string, inside sighting) (wrapper, bool) {
	var enclosing *declaration
	decls := f.declarations()
	for i := range decls {
		if decls[i].nameAt < at {
			enclosing = &decls[i]
		}
	}
	if enclosing == nil {
		return wrapper{}, false
	}
	for _, p := range parameters(f, *enclosing) {
		if p.name == ident {
			return wrapper{sdk: sdk, kind: inside.kind, name: enclosing.name, family: family(f.ext), param: p.position, label: p.label,
				file: f.rel, line: f.line(enclosing.nameAt), declAt: enclosing.nameAt, container: f.containerOf(enclosing.nameAt), inside: inside, home: f}, true
		}
	}
	return wrapper{}, false
}

var (
	tenjinReceiverBefore = regexp.MustCompile(`(?i)tenjin\w*(?:\s*\(\s*\))?\s*[!?]*\s*\.\s*$`)
	chainWord            = regexp.MustCompile(`[A-Za-z_$][\w$]*`)
)

// receiverAt returns the expression a member access whose dot is at dot is
// made on, such as AnalyticsService.shared, or "", and where it starts.
func receiverAt(code string, dot int) (int, string) {
	end := dot
	for end > 0 && (space(code[end-1]) || code[end-1] == '!' || code[end-1] == '?') {
		end--
	}
	start, depth := end, 0
	for start > 0 && end-start < 200 {
		c := code[start-1]
		switch {
		case c == ')' || c == ']':
			depth++
		case (c == '(' || c == '[') && depth > 0:
			depth--
		case depth > 0, c == '.' || c == '?' || c == '!' || c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
		default:
			return start, code[start:end]
		}
		start--
	}
	return start, code[start:end]
}

// receiverOf returns the expression a call whose name starts at at is made
// on, or "" for a call on nothing, and where it starts.
func receiverOf(code string, at int) (int, string) {
	p := at
	for p > 0 && space(code[p-1]) {
		p--
	}
	if p == 0 || code[p-1] != '.' {
		return at, ""
	}
	return receiverAt(code, p-1)
}

// extensionFunction matches what comes before the receiver of a Kotlin
// extension function's declaration.
var extensionFunction = regexp.MustCompile(`\bfun\s+(?:<[^>]*>\s*)?$`)

// declaresAt reports whether a function is declared with its name at at.
func (f *sourceFile) declaresAt(at int) bool {
	decls := f.declarations()
	i := sort.Search(len(decls), func(i int) bool { return decls[i].nameAt >= at })
	return i < len(decls) && decls[i].nameAt == at
}

// containerNames are the names a file reaches a wrapper's class or object
// by: the class itself, a variable of that type or made from it, or a
// JavaScript import of the module that declares it.
func (s *analyticsScan) containerNames(f *sourceFile, w *wrapper) map[string]bool {
	key := w.container + "\x00" + w.file
	if names, ok := f.names[key]; ok {
		return names
	}
	names := map[string]bool{w.container: true}
	module := strings.TrimSuffix(path.Base(w.file), path.Ext(w.file))
	if strings.Contains(f.text, w.container) || strings.Contains(f.text, module) {
		for _, re := range s.containerPatterns(w.container, module) {
			for _, m := range re.FindAllStringSubmatch(f.text, -1) {
				names[m[1]] = true
			}
		}
	}
	if f.names == nil {
		f.names = map[string]map[string]bool{}
	}
	f.names[key] = names
	return names
}

func (s *analyticsScan) containerPatterns(container, module string) []*regexp.Regexp {
	key := container + "\x00" + module
	if res, ok := s.patterns[key]; ok {
		return res
	}
	t := regexp.QuoteMeta(container)
	res := []*regexp.Regexp{
		regexp.MustCompile(`\b(\w+)\s*:\s*` + t + `\b`),
		regexp.MustCompile(`\b` + t + `\b[?!]?[ \t]+(\w+)\s*[=;,)]`),
		regexp.MustCompile(`\b` + t + `\s*\*\s*(\w+)`),
		regexp.MustCompile(`\b(\w+)\s*=\s*(?:new\s+)?` + t + `\b`),
		regexp.MustCompile(`import\s+(?:\*\s+as\s+)?(\w+)\s+from\s+['"][^'"]*\b` + regexp.QuoteMeta(module) + `['"]`),
	}
	s.patterns[key] = res
	return res
}

// callsWrapper reports whether a call at at in a file, made on recv, is to
// the wrapper. Made on something, the call is to it when that is its class
// or object, or a name the file has for them. Made on nothing, the call is
// to it when nothing closer of that name is declared around the call, and
// the file can call it: in its own class, or, for a function of its own,
// in the same file, the same Kotlin package or Swift module, or a file
// that imports it.
func (s *analyticsScan) callsWrapper(f *sourceFile, w *wrapper, recv string, at int) bool {
	same := f.rel == w.file
	if recv == "" {
		if shadowed(f, w, at) {
			return false
		}
		if w.container != "" {
			return same
		}
		return same || reaches(f, w)
	}
	if w.container == "" {
		return false
	}
	words := chainWord.FindAllString(recv, -1)
	for _, word := range words {
		if word == w.container || (same && (word == "this" || word == "self")) {
			return true
		}
	}
	names := s.containerNames(f, w)
	for _, word := range words {
		if names[word] {
			return true
		}
	}
	return false
}

// shadowed reports whether a call made on nothing at at is to another
// function of the wrapper's name: one declared in a container around the
// call, or, for a function of its own, one the file declares itself.
// Overloads of the wrapper, beside it, are not other functions.
func shadowed(f *sourceFile, w *wrapper, at int) bool {
	var own *scope
	if f.rel == w.file {
		own = f.innermost(w.declAt)
	}
	for _, d := range f.declarations() {
		if d.name != w.name || (f.rel == w.file && d.nameAt == w.declAt) {
			continue
		}
		in := f.innermost(d.nameAt)
		switch {
		case in == nil:
			if f.rel != w.file && w.container == "" {
				return true
			}
		case own != nil && in.open == own.open:
		case in.open <= at && at < in.close:
			return true
		}
	}
	return false
}

// reaches reports whether a file can call a function of its own, declared
// in another file, by its name alone.
func reaches(f *sourceFile, w *wrapper) bool {
	switch w.family {
	case ".swift":
		return true
	case "jvm":
		if f.ext != ".kt" || w.home == nil || path.Ext(w.file) != ".kt" {
			return false
		}
		pkg := w.home.pkg()
		if f.pkg() == pkg {
			return true
		}
		for _, imp := range f.imports() {
			if imp == pkg+"."+w.name || imp == pkg+".*" {
				return true
			}
		}
	case ".dart":
		for _, imp := range f.imports() {
			if path.Base(imp) == path.Base(w.file) {
				return true
			}
		}
	case "js":
		return importsFunction(f, w)
	}
	return false
}

var jsImport = regexp.MustCompile(`import\s+(\w+)?\s*,?\s*(?:\{([^}]*)\})?\s*from\s*['"]([^'"]+)['"]|(?:const|let|var)\s*\{([^}]*)\}\s*=\s*require\(\s*['"]([^'"]+)['"]\s*\)`)

// importsFunction reports whether a JavaScript file imports the wrapper,
// under its own name, from the module that declares it.
func importsFunction(f *sourceFile, w *wrapper) bool {
	module := strings.TrimSuffix(path.Base(w.file), path.Ext(w.file))
	if module == "index" {
		module = path.Base(path.Dir(w.file))
	}
	for _, m := range jsImport.FindAllStringSubmatch(f.text, -1) {
		from := m[3] + m[5]
		if strings.TrimSuffix(path.Base(from), path.Ext(path.Base(from))) != module && path.Base(from) != module {
			continue
		}
		if m[1] == w.name {
			return true
		}
		for _, item := range strings.Split(m[2]+","+m[4], ",") {
			if fields := strings.Fields(item); len(fields) == 1 && fields[0] == w.name {
				return true
			}
		}
	}
	return false
}

// followWrappers finds the calls to the app's own functions that hand a
// name or an ID to an SDK, and reads what each call gives. A wrapper nobody
// calls is reported as the call inside it.
func (s *analyticsScan) followWrappers() {
	seen := map[string]bool{}
	for i := range s.wrappers {
		w := &s.wrappers[i]
		// The same SDK called twice inside one wrapper is one wrapper.
		id := fmt.Sprint(w.sdk.name, w.kind, w.file, w.line, w.param, w.label)
		if seen[id] {
			continue
		}
		seen[id] = true
		for _, f := range s.files {
			if family(f.ext) != w.family {
				continue
			}
			s.callersIn(f, w)
		}
		if w.callers == 0 {
			s.sightings = append(s.sightings, w.inside)
		}
	}
}

// callersIn reads the calls to a wrapper in one file.
func (s *analyticsScan) callersIn(f *sourceFile, w *wrapper) {
	if w.family == "objc" {
		for _, c := range f.sends()[w.name] {
			if s.sdkCalls[spot{f.index, c.at}] || !s.callsWrapper(f, w, f.code[c.recvStart:c.recvEnd], c.at) {
				continue
			}
			if args := splitArgs(f.text, c.arg, true); len(args) > 0 {
				s.addFromWrapper(f, w, args[0])
			}
		}
		return
	}
	for _, c := range f.calls()[w.name] {
		if s.sdkCalls[spot{f.index, c.at}] || tenjinReceiverBefore.MatchString(f.code[max(0, c.at-60):c.at]) {
			continue
		}
		// A declaration is not a call: fun Foo.name( on something, name( on
		// nothing.
		recv := ""
		if c.member {
			var start int
			if start, recv = receiverOf(f.code, c.at); recv == "" || extensionFunction.MatchString(f.code[max(0, start-80):start]) {
				continue
			}
		} else if f.declaresAt(c.at) {
			continue
		}
		if !s.callsWrapper(f, w, recv, c.at) {
			continue
		}
		args := splitArgs(f.text, c.open, false)
		if arg, ok := pick(f.text, args, w); ok {
			s.addFromWrapper(f, w, arg)
		}
	}
}

// pick returns the argument a call to a wrapper gives its parameter.
func pick(text string, args []span, w *wrapper) (span, bool) {
	if w.label != "" {
		for _, a := range args {
			if labelled(text[a.start:a.end], w.label) {
				return a, true
			}
		}
		return span{}, false
	}
	position := 0
	for _, a := range args {
		if argLabel.MatchString(strings.TrimSpace(text[a.start:a.end])) && w.family != "jvm" && w.family != "js" {
			continue
		}
		if position == w.param {
			return a, true
		}
		position++
	}
	return span{}, false
}

// addFromWrapper records what a call to a wrapper gives it. A call that
// gives nothing, such as null, is not one of the wrapper's uses: when all of
// them are such, the call inside it is reported.
func (s *analyticsScan) addFromWrapper(f *sourceFile, w *wrapper, a span) {
	before := len(s.sightings)
	s.add(f, w.sdk, w.kind, a.start, f.text[a.start:a.end], w.name+" ("+w.file+":"+strconv.Itoa(w.line)+")")
	if len(s.sightings) > before {
		w.callers++
	}
}
