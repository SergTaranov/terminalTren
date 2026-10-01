package vshell

import (
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ---- AST ----

type awkExpr interface{}

type awkNum float64
type awkStr string
type awkVar string
type awkRe string // regex literal /re/
type awkField struct{ idx awkExpr }
type awkBin struct {
	op   string
	l, r awkExpr
}
type awkUn struct {
	op string
	x  awkExpr
}
type awkCond struct{ c, t, f awkExpr } // c ? t : f
type awkMatch struct {
	l, r awkExpr
	neg  bool // !~
}
type awkCall struct {
	name string
	args []awkExpr
}

type awkStmt interface{}

type awkPrint struct{ args []awkExpr } // empty = print $0
type awkPrintf struct{ args []awkExpr }
type awkAssign struct {
	target awkExpr // awkVar | awkField
	op     string  // = += -= *= /= %=
	value  awkExpr
}
type awkIncDec struct {
	target awkExpr
	delta  int
}
type awkIf struct {
	cond      awkExpr
	then, els []awkStmt
}
type awkWhile struct {
	cond awkExpr
	body []awkStmt
}
type awkFor struct {
	init, cond, post awkStmt
	body             []awkStmt
}
type awkExit struct{ code awkExpr }
type awkBreak struct{}
type awkCont struct{}
type awkNext struct{}
type awkExprStmt struct{ expr awkExpr }

type awkItem struct {
	hasPattern bool
	pattern    awkExpr
	block      []awkStmt // nil = default action (print)
}

type awkProgram struct {
	begin []awkStmt
	items []awkItem
	end   []awkStmt
}

func awkCmds() []Cmd {
	return []Cmd{
		{
			Name: "awk",
			Man: `awk — обработка текста по полям
Синтаксис: awk [-F РАЗД] 'ПРОГРАММА' [файл...]
Поля строки: $1, $2... $NF (последнее). Всего: NF, строк: NR.
Программа: 'ШАБЛОН { ДЕЙСТВИЕ }'
Примеры:
  awk '{print $2}' file          — 2-е поле
  awk -F: '{print $1}' passwd    — до двоеточия
  awk '$3 > 100' data.txt        — фильтр
  awk 'END{print NR}' file       — число строк
  awk '{s+=$1} END{print s}'     — сумма колонки`,
			Run: awkRun,
		},
	}
}

func awkRun(c *Ctx) int {
	s := c.Shell
	_, vals, rest, err := parseArgs("F:v:", c.Args[1:], nil)
	if err != nil {
		return errPlain(c, err.Error())
	}
	if len(rest) == 0 {
		return errPlain(c, "no program given")
	}
	prog, perr := parseAwk(rest[0])
	if perr != nil {
		return errPlain(c, perr.Error())
	}

	interp := &awkInterp{
		shell:  s,
		vars:   map[string]any{},
		out:    c.Stdout,
		errOut: c.Stderr,
	}
	interp.vars["FS"] = " "
	interp.vars["OFS"] = " "
	if v, ok := vals['F']; ok && v != "" {
		// awk interprets escapes in -F: -F'\t' means a real TAB
		interp.vars["FS"] = unescape(v)
	}
	if v, ok := vals['v']; ok {
		if eq := strings.IndexByte(v, '='); eq > 0 {
			interp.vars[v[:eq]] = v[eq+1:]
		}
	}

	exitCode := 0
	if code := interp.execBlock(prog.begin, c); interp.exited {
		exitCode = code
	} else {
		inputs, _ := collectInputs(c, s, rest[1:])
	mainLoop:
		for _, in := range inputs {
			for _, line := range splitLines(in) {
				interp.setRecord(line)
				for _, item := range prog.items {
					if item.hasPattern && !interp.truthy(interp.eval(item.pattern)) {
						continue
					}
					if item.block == nil {
						fmt.Fprintln(interp.out, line)
						continue
					}
					if c2 := interp.execBlock(item.block, c); interp.exited {
						exitCode = c2
						break mainLoop
					}
					if interp.nexted {
						// `next`: skip the remaining rules for this record
						interp.nexted = false
						break
					}
				}
			}
		}
	}
	// END runs even after `exit` in BEGIN or the main loop (POSIX awk),
	// but an `exit` inside END must not re-enter it
	if !interp.endRun {
		interp.endRun = true
		mainExit := exitCode
		interp.brk, interp.cont, interp.nexted = false, false, false
		interp.exited = false // let the END block run
		endCode := interp.execBlock(prog.end, c)
		if interp.exited {
			exitCode = endCode // `exit N` inside END wins
		} else {
			exitCode = mainExit
		}
	}
	return exitCode
}

// ---- interpreter ----

type awkInterp struct {
	shell   *Shell
	vars    map[string]any
	fields  []string
	record  string
	nr      int
	out     io.Writer
	errOut  io.Writer
	exited  bool
	exitVal int
	brk     bool
	cont    bool
	nexted  bool
	endRun  bool
}

func (a *awkInterp) setRecord(line string) {
	a.record = line
	a.nr++
	fs := toStr(a.vars["FS"])
	a.fields = splitAwkFields(line, fs)
	a.vars["NF"] = awkNum(len(a.fields))
	a.vars["NR"] = awkNum(a.nr)
}

// splitAwkFields splits a record; an empty record has no fields (NF=0),
// even with an explicit FS.
func splitAwkFields(line, fs string) []string {
	if line == "" {
		return nil
	}
	if fs == " " {
		return strings.Fields(line)
	}
	return strings.Split(line, fs)
}

func (a *awkInterp) execBlock(stmts []awkStmt, c *Ctx) int {
	code := 0
	for _, st := range stmts {
		if a.exited || a.brk || a.cont || a.nexted {
			break
		}
		code = a.execStmt(st, c)
	}
	return code
}

func (a *awkInterp) execStmt(st awkStmt, c *Ctx) int {
	switch t := st.(type) {
	case awkPrint:
		if len(t.args) == 0 {
			fmt.Fprintln(a.out, a.record)
			return 0
		}
		parts := make([]string, len(t.args))
		for i, e := range t.args {
			parts[i] = toStr(a.eval(e))
		}
		fmt.Fprintln(a.out, strings.Join(parts, toStr(a.vars["OFS"])))
	case awkPrintf:
		args := make([]string, len(t.args))
		for i, e := range t.args {
			args[i] = toStr(a.eval(e))
		}
		if len(args) == 0 {
			return 0
		}
		fmt.Fprint(a.out, printfFormat(unescape(args[0]), args[1:]))
	case awkAssign:
		val := a.eval(t.value)
		a.assignTo(t.target, t.op, val)
	case awkIncDec:
		cur := a.eval(t.target)
		a.assignTo(t.target, "=", awkNum(toNum(cur)+float64(t.delta)))
	case awkIf:
		if a.truthy(a.eval(t.cond)) {
			return a.execBlock(t.then, c)
		}
		return a.execBlock(t.els, c)
	case awkNext:
		a.nexted = true
		return 0
	case awkWhile:
		exhausted := true
		for guard := 0; guard < 1_000_000; guard++ {
			if !a.truthy(a.eval(t.cond)) {
				exhausted = false
				break
			}
			a.execBlock(t.body, c)
			if a.brk {
				a.brk = false
				exhausted = false
				break
			}
			a.cont = false
		}
		if exhausted {
			// safety valve tripped: say so instead of silently
			// truncating the loop
			fmt.Fprint(a.errOut, "awk: loop iteration limit exceeded\n")
		}
	case awkFor:
		if t.init != nil {
			a.execStmt(t.init, c)
		}
		exhausted := true
		for guard := 0; guard < 1_000_000; guard++ {
			if t.cond != nil && !a.truthy(a.eval(t.cond)) {
				exhausted = false
				break
			}
			a.execBlock(t.body, c)
			if a.brk {
				a.brk = false
				exhausted = false
				break
			}
			a.cont = false
			if t.post != nil {
				a.execStmt(t.post, c)
			}
		}
		if exhausted {
			fmt.Fprint(a.errOut, "awk: loop iteration limit exceeded\n")
		}
	case awkExit:
		code := 0
		if t.code != nil {
			code = int(toNum(a.eval(t.code)))
		}
		a.exited = true
		a.exitVal = code
		return code
	case awkBreak:
		a.brk = true
	case awkCont:
		a.cont = true
	case awkExprStmt:
		a.eval(t.expr)
	}
	return 0
}

func (a *awkInterp) assignTo(target awkExpr, op string, val any) {
	var cur any
	switch t := target.(type) {
	case awkVar:
		cur = a.vars[string(t)]
	case awkField:
		cur = a.fieldValue(t)
	default:
		return
	}
	switch op {
	case "=":
		// keep the numeric-ness of the value
	case "+=":
		val = awkNum(toNum(cur) + toNum(val))
	case "-=":
		val = awkNum(toNum(cur) - toNum(val))
	case "*=":
		val = awkNum(toNum(cur) * toNum(val))
	case "/=":
		if toNum(val) != 0 {
			val = awkNum(toNum(cur) / toNum(val))
		}
	case "%=":
		if toNum(val) != 0 {
			val = awkNum(float64(int64(toNum(cur)) % int64(toNum(val))))
		}
	}
	switch t := target.(type) {
	case awkVar:
		a.vars[string(t)] = val
	case awkField:
		idx := int(toNum(a.eval(t.idx)))
		if idx < 0 {
			return // negative field: no-op instead of a panic
		}
		if idx == 0 {
			a.record = toStr(val)
			a.setRecordNoCount(a.record)
			return
		}
		for len(a.fields) < idx {
			a.fields = append(a.fields, "")
		}
		a.fields[idx-1] = toStr(val)
		a.record = strings.Join(a.fields, toStr(a.vars["OFS"]))
		a.setRecordNoCount(a.record)
	}
}

func (a *awkInterp) setRecordNoCount(line string) {
	fs := toStr(a.vars["FS"])
	a.fields = splitAwkFields(line, fs)
	a.vars["NF"] = awkNum(len(a.fields))
}

func (a *awkInterp) fieldValue(f awkField) any {
	idx := int(toNum(a.eval(f.idx)))
	if idx == 0 {
		return a.record
	}
	if idx < 0 || idx > len(a.fields) {
		return awkStr("") // negative/out-of-range field: empty, not a crash
	}
	return awkStr(a.fields[idx-1])
}

var awkRegexCache = map[string]*regexp.Regexp{}

// regex compiles and caches a regex, quoting it as a literal on failure.
func (a *awkInterp) regex(src string) *regexp.Regexp {
	if re, ok := awkRegexCache[src]; ok {
		return re
	}
	re, err := regexp.Compile(src)
	if err != nil {
		re = regexp.MustCompile(regexp.QuoteMeta(src))
	}
	awkRegexCache[src] = re
	return re
}

func (a *awkInterp) eval(e awkExpr) any {
	switch t := e.(type) {
	case awkNum:
		return t
	case awkStr:
		return t
	case awkVar:
		name := string(t)
		switch name {
		case "NF":
			return awkNum(len(a.fields))
		case "NR":
			return awkNum(a.nr)
		}
		if v, ok := a.vars[name]; ok {
			return v
		}
		return awkStr("")
	case awkField:
		return a.fieldValue(t)
	case awkRe:
		re := a.regex(string(t))
		return awkNum(b2f(re.MatchString(a.record)))
	case awkBin:
		l := a.eval(t.l)
		r := a.eval(t.r)
		return binOpValue(t.op, l, r)
	case awkUn:
		v := a.eval(t.x)
		switch t.op {
		case "!":
			return awkNum(b2f(!a.truthy(v)))
		case "-":
			return awkNum(-toNum(v))
		case "+":
			return awkNum(toNum(v))
		}
		return awkStr("")
	case awkCond:
		if a.truthy(a.eval(t.c)) {
			return a.eval(t.t)
		}
		return a.eval(t.f)
	case awkMatch:
		l := toStr(a.eval(t.l))
		var src string
		if re, ok := t.r.(awkRe); ok {
			src = string(re)
		} else {
			src = toStr(a.eval(t.r))
		}
		m := a.regex(src).MatchString(l)
		if t.neg {
			m = !m
		}
		return awkNum(b2f(m))
	case awkCall:
		return a.callBuiltin(t)
	}
	return awkStr("")
}

func (a *awkInterp) callBuiltin(f awkCall) any {
	arg := func(i int) string {
		if i < len(f.args) {
			return toStr(a.eval(f.args[i]))
		}
		return ""
	}
	argNum := func(i int) float64 {
		if i < len(f.args) {
			return toNum(a.eval(f.args[i]))
		}
		return 0
	}
	switch f.name {
	case "length":
		if len(f.args) == 0 {
			return awkNum(len(a.record))
		}
		return awkNum(len([]rune(arg(0))))
	case "substr":
		s := []rune(arg(0))
		start := int(argNum(1))
		if start < 1 {
			start = 1
		}
		if start > len(s) {
			return awkStr("")
		}
		end := len(s)
		if len(f.args) > 2 {
			l := int(argNum(2))
			if l < 0 {
				l = 0
			}
			if start-1+l < end {
				end = start - 1 + l
			}
		}
		return awkStr(string(s[start-1 : end]))
	case "index":
		return awkNum(float64(strings.Index(arg(0), arg(1)) + 1))
	case "tolower":
		return awkStr(strings.ToLower(arg(0)))
	case "toupper":
		return awkStr(strings.ToUpper(arg(0)))
	case "int":
		return awkNum(float64(int64(argNum(0))))
	case "sprintf":
		args := make([]string, len(f.args))
		for i := range f.args {
			args[i] = toStr(a.eval(f.args[i]))
		}
		if len(args) == 0 {
			return awkStr("")
		}
		return awkStr(printfFormat(unescape(args[0]), args[1:]))
	case "sqrt":
		return awkNum(sqrt64(argNum(0)))
	case "sub", "gsub":
		if len(f.args) < 2 {
			return awkNum(0)
		}
		var src string
		if re, ok := f.args[0].(awkRe); ok {
			src = string(re)
		} else {
			src = toStr(a.eval(f.args[0]))
		}
		repl := toStr(a.eval(f.args[1]))
		var target awkExpr = awkField{idx: awkNum(0)}
		if len(f.args) > 2 {
			target = f.args[2]
		}
		count, newval := awkRegexReplace(a.regex(src), toStr(a.eval(target)), repl, f.name == "gsub")
		a.assignTo(target, "=", awkStr(newval))
		return awkNum(float64(count))
	case "match":
		if len(f.args) < 2 {
			return awkNum(0)
		}
		var src string
		if re, ok := f.args[1].(awkRe); ok {
			src = string(re)
		} else {
			src = toStr(a.eval(f.args[1]))
		}
		if loc := a.regex(src).FindStringIndex(arg(0)); loc != nil {
			a.vars["RSTART"] = awkNum(float64(loc[0] + 1))
			a.vars["RLENGTH"] = awkNum(float64(loc[1] - loc[0]))
			return awkNum(1)
		}
		a.vars["RSTART"] = awkNum(0)
		a.vars["RLENGTH"] = awkNum(-1)
		return awkNum(0)
	}
	// unknown builtins fail loudly instead of silently returning ""
	// (real awk: "calling undefined function", fatal)
	fmt.Fprintf(a.errOut, "awk: calling undefined function %s\n", f.name)
	a.exited = true
	a.exitVal = 2
	return awkStr("")
}

// awkRegexReplace replaces matches of re in s with repl, where & stands
// for the whole match and \& for a literal &. gsub replaces all matches,
// sub only the first. Returns the number of replacements made.
func awkRegexReplace(re *regexp.Regexp, s, repl string, all bool) (int, string) {
	locs := re.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return 0, s
	}
	if !all {
		locs = locs[:1]
	}
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		b.WriteString(s[last:loc[0]])
		for i := 0; i < len(repl); i++ {
			switch {
			case repl[i] == '&':
				b.WriteString(s[loc[0]:loc[1]])
			case repl[i] == '\\' && i+1 < len(repl):
				b.WriteByte(repl[i+1])
				i++
			default:
				b.WriteByte(repl[i])
			}
		}
		last = loc[1]
	}
	b.WriteString(s[last:])
	return len(locs), b.String()
}

func (a *awkInterp) truthy(v any) bool {
	switch t := v.(type) {
	case awkNum:
		return t != 0
	case float64:
		return t != 0
	case string:
		return t != "" && t != "0"
	case awkStr:
		return t != "" && t != "0"
	}
	return false
}

// ---- value helpers ----

func toStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case awkStr:
		return string(t)
	case float64:
		return fmtNum(t)
	case awkNum:
		return fmtNum(float64(t))
	}
	return ""
}

func fmtNum(f float64) string {
	if f == float64(int64(f)) && f < 1e15 && f > -1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', 6, 64)
}

func toNum(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case awkNum:
		return float64(t)
	case string:
		return numPrefix(t)
	case awkStr:
		return numPrefix(string(t))
	}
	return 0
}

// numPrefix coerces a string to a number by its leading numeric prefix,
// like awk: "12kg" -> 12, "3.5abc" -> 3.5, "" -> 0.
func numPrefix(s string) float64 {
	s = strings.TrimLeft(s, " \t\n\r")
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits, dots := 0, 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.' && dots == 0) {
		if s[i] == '.' {
			dots++
		}
		digits++
		i++
	}
	if digits == 0 {
		return 0
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		k := j
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		if k > j {
			i = k
		}
	}
	f, _ := strconv.ParseFloat(s[:i], 64)
	return f
}

// looksNumeric reports whether a string is fully a number (for comparisons).
func looksNumeric(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func binOpValue(op string, l, r any) any {
	switch op {
	case "+", "-", "*", "/", "%":
		ln, rn := toNum(l), toNum(r)
		switch op {
		case "+":
			return awkNum(ln + rn)
		case "-":
			return awkNum(ln - rn)
		case "*":
			return awkNum(ln * rn)
		case "/":
			if rn == 0 {
				return awkNum(0)
			}
			return awkNum(ln / rn)
		default:
			if rn == 0 {
				return awkNum(0)
			}
			return awkNum(float64(int64(ln) % int64(rn)))
		}
	case "||":
		if truthyAny(l) || truthyAny(r) {
			return awkNum(1)
		}
		return awkNum(0)
	case "&&":
		if truthyAny(l) && truthyAny(r) {
			return awkNum(1)
		}
		return awkNum(0)
	case "==", "!=", "<", "<=", ">", ">=":
		return awkNum(b2f(compareVals(op, l, r)))
	}
	// default: concatenation
	return awkStr(toStr(l) + toStr(r))
}

func truthyAny(v any) bool {
	switch t := v.(type) {
	case float64:
		return t != 0
	case awkNum:
		return t != 0
	case string:
		return t != "" && t != "0"
	case awkStr:
		return t != "" && t != "0"
	}
	return false
}

func compareVals(op string, l, r any) bool {
	ls, lIsStr := vString(l)
	rs, rIsStr := vString(r)
	// numeric comparison only when both operands are numeric (numbers or
	// numeric-looking strings); a number vs a non-numeric string compares
	// as strings, like POSIX awk
	numeric := looksNumeric(ls) && looksNumeric(rs)
	if !numeric && !lIsStr && !rIsStr {
		numeric = true
	}
	if numeric {
		ln, rn := toNum(l), toNum(r)
		switch op {
		case "==":
			return ln == rn
		case "!=":
			return ln != rn
		case "<":
			return ln < rn
		case "<=":
			return ln <= rn
		case ">":
			return ln > rn
		default:
			return ln >= rn
		}
	}
	switch op {
	case "==":
		return ls == rs
	case "!=":
		return ls != rs
	case "<":
		return ls < rs
	case "<=":
		return ls <= rs
	case ">":
		return ls > rs
	default:
		return ls >= rs
	}
}

func vString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case awkStr:
		return string(t), true
	}
	return toStr(v), false
}

func sqrt64(f float64) float64 {
	if f < 0 {
		return math.NaN()
	}
	return math.Sqrt(f)
}

// ---- lexer ----

const (
	tkNum = iota
	tkStr
	tkID
	tkOp // operators and punctuation
	tkRegex
	tkEOF
)

type awkToken struct {
	kind int
	text string
	num  float64
}

var awkKeywords = map[string]bool{
	"print": true, "printf": true, "if": true, "else": true, "while": true,
	"for": true, "do": true, "in": true, "exit": true, "break": true,
	"continue": true, "next": true, "BEGIN": true, "END": true,
}

func lexAwk(src string) ([]awkToken, error) {
	var toks []awkToken
	i := 0
	prevOperand := false
	for i < len(src) {
		ch := src[i]
		switch {
		case ch == ' ' || ch == '\t' || ch == '\r':
			i++
		case ch == '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case ch == '\n' || ch == ';':
			toks = append(toks, awkToken{kind: tkOp, text: "\n"})
			prevOperand = false
			i++
		case ch >= '0' && ch <= '9' || ch == '.' && i+1 < len(src) && src[i+1] >= '0' && src[i+1] <= '9':
			j := i
			for j < len(src) && (src[j] >= '0' && src[j] <= '9' || src[j] == '.') {
				j++
			}
			n, _ := strconv.ParseFloat(src[i:j], 64)
			toks = append(toks, awkToken{kind: tkNum, num: n, text: src[i:j]})
			prevOperand = true
			i = j
		case isAlnum(ch) || ch == '_' || ch >= 0x80:
			j := i
			for j < len(src) && (isAlnum(src[j]) || src[j] == '_' || src[j] >= 0x80) {
				j++
			}
			toks = append(toks, awkToken{kind: tkID, text: src[i:j]})
			prevOperand = true
			i = j
		case ch == '"':
			var b strings.Builder
			i++
			for i < len(src) && src[i] != '"' {
				if src[i] == '\\' && i+1 < len(src) {
					switch src[i+1] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					case '\\':
						b.WriteByte('\\')
					case '"':
						b.WriteByte('"')
					default:
						b.WriteByte(src[i+1])
					}
					i += 2
					continue
				}
				b.WriteByte(src[i])
				i++
			}
			i++
			toks = append(toks, awkToken{kind: tkStr, text: b.String()})
			prevOperand = true
		case ch == '/' && !prevOperand:
			// regex literal
			var b strings.Builder
			i++
			for i < len(src) && src[i] != '/' {
				if src[i] == '\\' && i+1 < len(src) {
					b.WriteByte(src[i])
					i++
				}
				b.WriteByte(src[i])
				i++
			}
			i++
			re := b.String()
			if _, err := regexp.Compile(re); err != nil {
				return nil, fmt.Errorf("bad regex /%s/", re)
			}
			toks = append(toks, awkToken{kind: tkRegex, text: re})
			prevOperand = true
		default:
			two := ""
			if i+1 < len(src) {
				two = src[i : i+2]
			}
			switch two {
			case "||", "&&", "==", "!=", "<=", ">=", "+=", "-=", "*=", "/=", "%=", "!~", "++", "--":
				toks = append(toks, awkToken{kind: tkOp, text: two})
				prevOperand = two == ")" || two == "]" || false
				i += 2
			default:
				toks = append(toks, awkToken{kind: tkOp, text: string(ch)})
				prevOperand = ch == ')' || ch == ']'
				i++
			}
		}
	}
	toks = append(toks, awkToken{kind: tkEOF})
	return toks, nil
}

// ---- parser ----

type awkParser struct {
	toks []awkToken
	pos  int
}

func parseAwk(src string) (*awkProgram, error) {
	toks, err := lexAwk(src)
	if err != nil {
		return nil, err
	}
	p := &awkParser{toks: toks}
	return p.parseProgram()
}

func (p *awkParser) peek() awkToken { return p.toks[p.pos] }

func (p *awkParser) next() awkToken {
	t := p.toks[p.pos]
	if t.kind != tkEOF {
		p.pos++
	}
	return t
}

func (p *awkParser) isOp(s string) bool {
	t := p.peek()
	return t.kind == tkOp && t.text == s
}

func (p *awkParser) skipSep() {
	for p.isOp("\n") {
		p.pos++
	}
}

func (p *awkParser) expectOp(s string) error {
	if !p.isOp(s) {
		return fmt.Errorf("expected '%s' near '%s'", s, p.peek().text)
	}
	p.pos++
	return nil
}

func (p *awkParser) parseProgram() (*awkProgram, error) {
	prog := &awkProgram{}
	for {
		p.skipSep()
		t := p.peek()
		if t.kind == tkEOF {
			break
		}
		if t.kind == tkID && t.text == "BEGIN" && p.peekAt(1).kind == tkOp && p.peekAt(1).text == "{" {
			p.next()
			block, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			prog.begin = block
			continue
		}
		if t.kind == tkID && t.text == "END" && p.peekAt(1).kind == tkOp && p.peekAt(1).text == "{" {
			p.next()
			block, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			prog.end = block
			continue
		}
		// pattern (optional) + action
		var item awkItem
		if !p.isOp("{") {
			pat, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			item.hasPattern = true
			item.pattern = pat
		}
		p.skipSep()
		if p.isOp("{") {
			block, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			item.block = block
		}
		if !item.hasPattern && item.block == nil {
			return nil, fmt.Errorf("syntax error near '%s'", p.peek().text)
		}
		prog.items = append(prog.items, item)
	}
	return prog, nil
}

func (p *awkParser) peekAt(n int) awkToken {
	if p.pos+n < len(p.toks) {
		return p.toks[p.pos+n]
	}
	return awkToken{kind: tkEOF}
}

func (p *awkParser) parseBlock() ([]awkStmt, error) {
	if err := p.expectOp("{"); err != nil {
		return nil, err
	}
	var stmts []awkStmt
	for {
		p.skipSep()
		if p.isOp("}") {
			p.pos++
			return stmts, nil
		}
		if p.peek().kind == tkEOF {
			return nil, fmt.Errorf("missing '}'")
		}
		st, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		if st != nil {
			stmts = append(stmts, st)
		}
	}
}

func (p *awkParser) parseStmt() (awkStmt, error) {
	t := p.peek()
	if t.kind == tkID {
		switch t.text {
		case "print":
			p.next()
			var args []awkExpr
			for !p.atStmtEnd() {
				e, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				args = append(args, e)
				if p.isOp(",") {
					p.pos++
					continue
				}
				break
			}
			p.endStmt()
			return awkPrint{args: args}, nil
		case "printf":
			p.next()
			var args []awkExpr
			for !p.atStmtEnd() {
				e, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				args = append(args, e)
				if p.isOp(",") {
					p.pos++
					continue
				}
				break
			}
			p.endStmt()
			return awkPrintf{args: args}, nil
		case "if":
			p.next()
			if err := p.expectOp("("); err != nil {
				return nil, err
			}
			cond, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if err := p.expectOp(")"); err != nil {
				return nil, err
			}
			then, err := p.parseStmtOrBlock()
			if err != nil {
				return nil, err
			}
			st := awkIf{cond: cond, then: then}
			p.skipSep()
			if p.peek().kind == tkID && p.peek().text == "else" {
				p.next()
				els, err := p.parseStmtOrBlock()
				if err != nil {
					return nil, err
				}
				st.els = els
			}
			return st, nil
		case "while":
			p.next()
			if err := p.expectOp("("); err != nil {
				return nil, err
			}
			cond, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if err := p.expectOp(")"); err != nil {
				return nil, err
			}
			body, err := p.parseStmtOrBlock()
			if err != nil {
				return nil, err
			}
			return awkWhile{cond: cond, body: body}, nil
		case "for":
			p.next()
			if err := p.expectOp("("); err != nil {
				return nil, err
			}
			init, err := p.parseSimpleStmtOrSkip()
			if err != nil {
				return nil, err
			}
			if err := p.expectOp(";"); err != nil {
				return nil, err
			}
			var cond awkExpr
			if !p.isOp(";") {
				cond, err = p.parseExpr()
				if err != nil {
					return nil, err
				}
			}
			if err := p.expectOp(";"); err != nil {
				return nil, err
			}
			var post awkStmt
			if !p.isOp(")") {
				post, err = p.parseSimpleStmt()
				if err != nil {
					return nil, err
				}
			}
			if err := p.expectOp(")"); err != nil {
				return nil, err
			}
			body, err := p.parseStmtOrBlock()
			if err != nil {
				return nil, err
			}
			return awkFor{init: init, cond: cond, post: post, body: body}, nil
		case "exit":
			p.next()
			var code awkExpr
			if !p.atStmtEnd() {
				code, _ = p.parseExpr()
			}
			p.endStmt()
			return awkExit{code: code}, nil
		case "break":
			p.next()
			p.endStmt()
			return awkBreak{}, nil
		case "continue":
			p.next()
			p.endStmt()
			return awkCont{}, nil
		case "next":
			p.next()
			p.endStmt()
			return awkNext{}, nil
		}
	}
	st, err := p.parseSimpleStmt()
	if err != nil {
		return nil, err
	}
	p.endStmt()
	return st, nil
}

// parseSimpleStmt parses assignment, ++/-- or a bare expression.
func (p *awkParser) parseSimpleStmt() (awkStmt, error) {
	e, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	switch e.(type) {
	case awkVar, awkField:
		if p.isOp("++") {
			p.pos++
			return awkIncDec{target: e, delta: 1}, nil
		}
		if p.isOp("--") {
			p.pos++
			return awkIncDec{target: e, delta: -1}, nil
		}
		for _, op := range []string{"=", "+=", "-=", "*=", "/=", "%="} {
			if p.isOp(op) {
				p.pos++
				rhs, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				return awkAssign{target: e, op: op, value: rhs}, nil
			}
		}
	}
	// any other expression is a valid statement, e.g. gsub(/a/, "b")
	// or match($0, /re/)
	return awkExprStmt{expr: e}, nil
}

func (p *awkParser) parseSimpleStmtOrSkip() (awkStmt, error) {
	if p.isOp(";") {
		return nil, nil
	}
	return p.parseSimpleStmt()
}

func (p *awkParser) parseStmtOrBlock() ([]awkStmt, error) {
	if p.isOp("{") {
		return p.parseBlock()
	}
	st, err := p.parseStmt()
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, nil
	}
	return []awkStmt{st}, nil
}

func (p *awkParser) atStmtEnd() bool {
	t := p.peek()
	return t.kind == tkEOF || t.kind == tkOp && (t.text == "\n" || t.text == "}" || t.text == ";")
}

func (p *awkParser) endStmt() {
	if p.isOp(";") || p.isOp("\n") {
		p.pos++
	}
}

// ---- expression parser ----

func (p *awkParser) parseExpr() (awkExpr, error) { return p.parseTernary() }

func (p *awkParser) parseTernary() (awkExpr, error) {
	c, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.isOp("?") {
		p.pos++
		t, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expectOp(":"); err != nil {
			return nil, err
		}
		f, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return awkCond{c: c, t: t, f: f}, nil
	}
	return c, nil
}

func (p *awkParser) parseOr() (awkExpr, error) {
	l, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.isOp("||") {
		p.pos++
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l = awkBin{op: "||", l: l, r: r}
	}
	return l, nil
}

func (p *awkParser) parseAnd() (awkExpr, error) {
	l, err := p.parseMatch()
	if err != nil {
		return nil, err
	}
	for p.isOp("&&") {
		p.pos++
		r, err := p.parseMatch()
		if err != nil {
			return nil, err
		}
		l = awkBin{op: "&&", l: l, r: r}
	}
	return l, nil
}

func (p *awkParser) parseMatch() (awkExpr, error) {
	l, err := p.parseCompare()
	if err != nil {
		return nil, err
	}
	for p.isOp("~") || p.isOp("!~") {
		neg := p.isOp("!~")
		p.pos++
		r, err := p.parseCompare()
		if err != nil {
			return nil, err
		}
		l = awkMatch{l: l, r: r, neg: neg}
	}
	return l, nil
}

func (p *awkParser) parseCompare() (awkExpr, error) {
	l, err := p.parseConcat()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind != tkOp {
			break
		}
		switch t.text {
		case "==", "!=", "<", "<=", ">", ">=":
			p.pos++
			r, err := p.parseConcat()
			if err != nil {
				return nil, err
			}
			l = awkBin{op: t.text, l: l, r: r}
		default:
			return l, nil
		}
	}
	return l, nil
}

func (p *awkParser) parseConcat() (awkExpr, error) {
	l, err := p.parseAdditive()
	if err != nil {
		return nil, err
	}
	for p.startsPrimary() {
		r, err := p.parseAdditive()
		if err != nil {
			return nil, err
		}
		l = awkBin{op: "concat", l: l, r: r}
	}
	return l, nil
}

func (p *awkParser) startsPrimary() bool {
	t := p.peek()
	switch t.kind {
	case tkNum, tkStr, tkRegex:
		return true
	case tkID:
		return !awkKeywords[t.text]
	case tkOp:
		return t.text == "$" || t.text == "("
	}
	return false
}

func (p *awkParser) parseAdditive() (awkExpr, error) {
	l, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for p.isOp("+") || p.isOp("-") {
		op := p.peek().text
		p.pos++
		r, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		l = awkBin{op: op, l: l, r: r}
	}
	return l, nil
}

func (p *awkParser) parseMul() (awkExpr, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.isOp("*") || p.isOp("/") || p.isOp("%") {
		op := p.peek().text
		p.pos++
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		l = awkBin{op: op, l: l, r: r}
	}
	return l, nil
}

func (p *awkParser) parseUnary() (awkExpr, error) {
	if p.isOp("!") || p.isOp("-") || p.isOp("+") {
		op := p.next().text
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return awkUn{op: op, x: x}, nil
	}
	return p.parsePrimary()
}

func (p *awkParser) parsePrimary() (awkExpr, error) {
	t := p.next()
	switch t.kind {
	case tkNum:
		return awkNum(t.num), nil
	case tkStr:
		return awkStr(t.text), nil
	case tkRegex:
		return awkRe(t.text), nil
	case tkID:
		if p.isOp("(") {
			p.pos++
			var args []awkExpr
			for !p.isOp(")") {
				e, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				args = append(args, e)
				if p.isOp(",") {
					p.pos++
				}
			}
			p.pos++
			return awkCall{name: t.text, args: args}, nil
		}
		return awkVar(t.text), nil
	case tkOp:
		switch t.text {
		case "$":
			idx, err := p.parsePrimary()
			if err != nil {
				return nil, err
			}
			return awkField{idx: idx}, nil
		case "(":
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if err := p.expectOp(")"); err != nil {
				return nil, err
			}
			return e, nil
		}
	}
	return nil, fmt.Errorf("unexpected token '%s'", t.text)
}
