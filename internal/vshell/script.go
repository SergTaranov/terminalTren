package vshell

import (
	"fmt"
	"strconv"
	"strings"
)

// ---- AST ----

type scriptStmt interface{}

type simpleStmt struct{ text string }

type ifStmt struct {
	conds  []string
	bodies [][]scriptStmt
	els    []scriptStmt
}

type forStmt struct {
	varName string
	words   string // raw, expanded at runtime
	body    []scriptStmt
}

type whileStmt struct {
	cond string
	body []scriptStmt
}

type funcStmt struct {
	name string
	body []scriptStmt
}

// localFrame holds the variables a function declared with `local`.
type localFrame struct {
	saved map[string]savedVar
}

func scriptCmds() []Cmd {
	return []Cmd{
		{
			Name: "bash",
			Man: `bash — запуск shell-скрипта
Синтаксис: bash скрипт.sh [аргументы...] | bash -c "команда"
Скрипту доступны $1, $2... — аргументы.`,
			Run: func(c *Ctx) int {
				_, vals, rest, err := parseArgs("c:", c.Args[1:], nil)
				if err != nil {
					return errPlain(c, err.Error())
				}
				if cmd, ok := vals['c']; ok {
					return c.Shell.runLine(cmd, c)
				}
				if len(rest) == 0 {
					return 0
				}
				sub := &Ctx{Shell: c.Shell, Args: rest, Stdout: c.Stdout, Stderr: c.Stderr}
				return runScriptFile(sub, false)
			},
		},
		{
			Name: "sh",
			Man:  `sh — POSIX shell (синоним bash в тренажёре)`,
			Run: func(c *Ctx) int {
				_, vals, rest, err := parseArgs("c:", c.Args[1:], nil)
				if err != nil {
					return errPlain(c, err.Error())
				}
				if cmd, ok := vals['c']; ok {
					return c.Shell.runLine(cmd, c)
				}
				if len(rest) == 0 {
					return 0
				}
				sub := &Ctx{Shell: c.Shell, Args: rest, Stdout: c.Stdout, Stderr: c.Stderr}
				return runScriptFile(sub, false)
			},
		},
		{
			Name: "source",
			Man: `source — выполнить скрипт в текущем shell
Синтаксис: source файл | . файл
В отличие от bash, изменения переменных и алиасов
сохраняются после выполнения (так читают .bashrc).`,
			Run: sourceRun,
		},
		{
			Name: ".",
			Man: `. — синоним source
Синтаксис: . файл`,
			Run: sourceRun,
		},
	}
}

func sourceRun(c *Ctx) int {
	s := c.Shell
	if len(c.Args) < 2 {
		return errPlain(c, "filename argument required")
	}
	content, rerr := s.FS.ReadFile(s.Cwd, c.Args[1])
	if rerr != nil {
		return errNode(c, c.Args[1], rerr)
	}
	stmts, perr := parseScript(content)
	if perr != nil {
		fmt.Fprintf(c.Stderr, "bash: %s: %s\n", c.Args[1], perr)
		return 2
	}
	oldParams := s.params
	if len(c.Args) > 2 {
		// `source script args...` overrides positional parameters;
		// otherwise the caller's parameters stay visible, like in bash
		s.params = c.Args[2:]
	}
	code, _ := s.execBlock(stmts, c)
	s.params = oldParams
	return code
}

// runScriptFile executes ./script.sh style invocations. A direct path
// needs the execute bit; `bash script.sh` only needs read access, like in
// real bash.
func runScriptFile(c *Ctx, requireExec bool) int {
	s := c.Shell
	pathArg := c.Args[0]
	n, err := s.FS.LookupNoFollow(s.Cwd, pathArg)
	if err != nil {
		fmt.Fprintf(c.Stderr, "bash: %s: %s\n", pathArg, err)
		return 127
	}
	if n.Type == TypeDir {
		fmt.Fprintf(c.Stderr, "bash: %s: %s\n", pathArg, ErrIsDir)
		return 126
	}
	if requireExec {
		if !CanExec(n, s.User) {
			fmt.Fprintf(c.Stderr, "bash: %s: Permission denied\n", pathArg)
			return 126
		}
	} else if !CanRead(n, s.User) {
		fmt.Fprintf(c.Stderr, "bash: %s: Permission denied\n", pathArg)
		return 126
	}
	stmts, perr := parseScript(n.Content)
	if perr != nil {
		fmt.Fprintf(c.Stderr, "bash: %s: %s\n", pathArg, perr)
		return 2
	}

	// child-process semantics: env, cwd, params, functions and aliases
	// do not leak back into the interactive shell
	savedEnv := make(map[string]string, len(s.Env))
	for k, v := range s.Env {
		savedEnv[k] = v
	}
	savedFuncs := make(map[string]interface{}, len(s.functions))
	for k, v := range s.functions {
		savedFuncs[k] = v
	}
	savedAliases := make(map[string]string, len(s.Aliases))
	for k, v := range s.Aliases {
		savedAliases[k] = v
	}
	savedCwd, savedParams := s.Cwd, s.params
	s.params = c.Args[1:]

	code, _ := s.execBlock(stmts, c)

	s.Env = savedEnv
	s.Cwd, s.Env["PWD"] = savedCwd, savedCwd
	s.params = savedParams
	s.functions = savedFuncs
	s.Aliases = savedAliases
	return code
}

// runLine executes one command line inside a script, writing output to c.
func (s *Shell) runLine(line string, c *Ctx) int {
	suppress := s.suppressHistory
	s.suppressHistory = true
	defer func() { s.suppressHistory = suppress }()
	return s.execCommandLine(line, c.Stdout, c.Stderr, &Result{})
}

// execBlock runs statements; the bool reports an `exit` was hit.
func (s *Shell) execBlock(stmts []scriptStmt, c *Ctx) (int, bool) {
	code := 0
	for _, st := range stmts {
		var exited bool
		switch t := st.(type) {
		case simpleStmt:
			line := strings.TrimSpace(t.text)
			if line == "" {
				continue
			}
			fields := strings.Fields(line)
			if fields[0] == "exit" {
				ec := 0
				if len(fields) > 1 {
					if v, err := strconv.Atoi(fields[1]); err == nil {
						ec = v
					}
				}
				return ec, true
			}
			if fields[0] == "local" {
				code = s.declareLocal(c, fields[1:])
				continue
			}
			code = s.runLine(line, c)
		case ifStmt:
			taken := false
			for i, cond := range t.conds {
				if s.runLine(cond, c) == 0 {
					code, exited = s.execBlock(t.bodies[i], c)
					taken = true
					if exited {
						return code, true
					}
					break
				}
			}
			if !taken {
				code, exited = s.execBlock(t.els, c)
				if exited {
					return code, true
				}
			}
		case forStmt:
			ran := false
			for _, w := range s.expandWords(t.words) {
				ran = true
				s.Env[t.varName] = w
				code, exited = s.execBlock(t.body, c)
				if exited {
					return code, true
				}
			}
			if !ran {
				code = 0 // bash: loop body never ran -> status 0
			}
		case whileStmt:
			ran := false
			for guard := 0; guard < 10000; guard++ {
				if s.runLine(t.cond, c) != 0 {
					break
				}
				ran = true
				code, exited = s.execBlock(t.body, c)
				if exited {
					return code, true
				}
			}
			if !ran {
				code = 0
			}
		case funcStmt:
			s.functions[t.name] = t.body
		}
	}
	return code, false
}

// expandWords expands a raw word list (vars, substitutions, globs and
// quotes) for `for x in WORDS`.
func (s *Shell) expandWords(raw string) []string {
	var words []string
	for _, t := range tokenize(raw, s.getenv, s.runSubshell) {
		if t.Kind != TokWord {
			continue
		}
		if !t.Quoted && hasWildcard(t.Text) {
			if matches := s.FS.Glob(s.Cwd, t.Text); len(matches) > 0 {
				for _, m := range matches {
					if !strings.HasPrefix(t.Text, "/") {
						m = strings.TrimPrefix(m, s.Cwd+"/")
					}
					words = append(words, m)
				}
				continue
			}
		}
		words = append(words, t.Text)
	}
	return words
}

// savedVar remembers a variable shadowed by a `local` declaration.
type savedVar struct {
	val     string
	existed bool
}

// declareLocal handles `local name[=value]...` inside a function body.
func (s *Shell) declareLocal(c *Ctx, args []string) int {
	if len(s.localFrames) == 0 {
		return errPlain(c, "local: can only be used in a function")
	}
	fr := s.localFrames[len(s.localFrames)-1]
	for _, a := range args {
		name, value := a, ""
		hasVal := false
		if eq := strings.IndexByte(a, '='); eq > 0 {
			name, value, hasVal = a[:eq], a[eq+1:], true
		}
		if name == "" {
			continue
		}
		if _, ok := fr.saved[name]; !ok {
			old, existed := s.Env[name]
			fr.saved[name] = savedVar{val: old, existed: existed}
		}
		if hasVal {
			s.Env[name] = value
		} else {
			s.Env[name] = ""
		}
	}
	return 0
}

// callFunction invokes a shell function defined in a script.
func (s *Shell) callFunction(name string, c *Ctx) (int, bool) {
	raw, ok := s.functions[name]
	if !ok {
		return 0, false
	}
	body, ok := raw.([]scriptStmt)
	if !ok {
		return 0, false
	}
	oldParams := s.params
	s.params = c.Args[1:]
	s.localFrames = append(s.localFrames, &localFrame{saved: map[string]savedVar{}})
	code, _ := s.execBlock(body, c)
	fr := s.localFrames[len(s.localFrames)-1]
	s.localFrames = s.localFrames[:len(s.localFrames)-1]
	for vname, sv := range fr.saved {
		if sv.existed {
			s.Env[vname] = sv.val
		} else {
			delete(s.Env, vname)
		}
	}
	s.params = oldParams
	return code, true
}

// ---- parser ----

func parseScript(src string) ([]scriptStmt, error) {
	p := &scriptParser{lines: splitScriptLines(src)}
	return p.parseBlock("", "")
}

func splitScriptLines(src string) []string {
	var out []string
	for _, l := range strings.Split(src, "\n") {
		l = strings.TrimSpace(stripComment(l))
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// stripComment removes a trailing # comment outside quotes.
func stripComment(line string) string {
	inS, inD := false, false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\'':
			if !inD {
				inS = !inS
			}
		case '"':
			if !inS {
				inD = !inD
			}
		case '#':
			if !inS && !inD && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
				return strings.TrimRight(line[:i], " \t")
			}
		}
	}
	return line
}

type scriptParser struct {
	lines []string
	pos   int
}

func (p *scriptParser) peek() (string, bool) {
	if p.pos >= len(p.lines) {
		return "", false
	}
	return p.lines[p.pos], true
}

func (p *scriptParser) next() (string, bool) {
	l, ok := p.peek()
	if ok {
		p.pos++
	}
	return l, ok
}

// parseBlock reads statements until one of the stop keywords (or EOF).
// openA/openB describe the construct for error messages.
func (p *scriptParser) parseBlock(openA, openB string) ([]scriptStmt, error) {
	var out []scriptStmt
	for {
		line, ok := p.peek()
		if !ok {
			if openA != "" {
				return nil, fmt.Errorf("syntax error: unexpected end of file, expected '%s'", openB)
			}
			return out, nil
		}
		first := firstWord(line)
		switch first {
		case "fi", "done", "else", "elif", "}":
			if openA != "" {
				return out, nil
			}
			return nil, fmt.Errorf("syntax error near unexpected token '%s'", first)
		case "if":
			p.next()
			st, err := p.parseIf(line)
			if err != nil {
				return nil, err
			}
			out = append(out, st)
		case "for":
			p.next()
			st, err := p.parseFor(line)
			if err != nil {
				return nil, err
			}
			out = append(out, st)
		case "while":
			p.next()
			st, err := p.parseWhile(line)
			if err != nil {
				return nil, err
			}
			out = append(out, st)
		case "case":
			return nil, fmt.Errorf("конструкция case не поддерживается тренажёром")
		default:
			if isFuncHeader(line) {
				p.next()
				st, err := p.parseFunc(line)
				if err != nil {
					return nil, err
				}
				out = append(out, st)
				continue
			}
			p.next()
			out = append(out, simpleStmt{line})
		}
	}
}

func firstWord(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func isFuncHeader(line string) bool {
	i := strings.Index(line, "()")
	if i <= 0 {
		return false
	}
	name := strings.TrimSpace(line[:i])
	if name == "" || strings.ContainsAny(name, " \t'\"") {
		return false
	}
	rest := strings.TrimSpace(line[i+2:])
	return rest == "{" || rest == ""
}

func (p *scriptParser) parseFunc(line string) (scriptStmt, error) {
	name := strings.TrimSpace(line[:strings.Index(line, "()")])
	// skip an optional "{" that is on its own line
	if l, ok := p.peek(); ok && l == "{" {
		p.next()
	}
	body, err := p.parseBlock("func", "}")
	if err != nil {
		return nil, err
	}
	if l, ok := p.peek(); ok && l == "}" {
		p.next()
	}
	return funcStmt{name: name, body: body}, nil
}

// condOf extracts "if COND" handling "; then" / "then" on the next line.
func (p *scriptParser) condOf(line, keyword, endKw string) (string, error) {
	rest := strings.TrimSpace(strings.TrimPrefix(line, keyword))
	for _, suffix := range []string{"; " + endKw, endKw} {
		if strings.HasSuffix(rest, suffix) {
			return strings.TrimSpace(strings.TrimSuffix(rest, suffix)), nil
		}
	}
	// expect endKw on a following line
	for {
		l, ok := p.next()
		if !ok {
			return "", fmt.Errorf("syntax error: expected '%s'", endKw)
		}
		l = strings.TrimSpace(l)
		if l == endKw {
			return rest, nil
		}
		if strings.HasSuffix(l, "; "+endKw) {
			return rest + " " + strings.TrimSpace(strings.TrimSuffix(l, "; "+endKw)), nil
		}
		if rest == "" {
			rest = l
		} else {
			rest = rest + " " + l
		}
	}
}

func (p *scriptParser) parseIf(line string) (scriptStmt, error) {
	st := ifStmt{}
	cond, err := p.condOf(line, "if", "then")
	if err != nil {
		return nil, err
	}
	st.conds = append(st.conds, cond)
	for {
		body, err := p.parseBlock("if", "fi")
		if err != nil {
			return nil, err
		}
		st.bodies = append(st.bodies, body)
		kw, ok := p.next()
		if !ok {
			return nil, fmt.Errorf("syntax error: expected 'fi'")
		}
		switch firstWord(kw) {
		case "fi":
			return st, nil
		case "elif":
			c2, err := p.condOf(kw, "elif", "then")
			if err != nil {
				return nil, err
			}
			st.conds = append(st.conds, c2)
		case "else":
			els, err := p.parseBlock("else", "fi")
			if err != nil {
				return nil, err
			}
			st.els = els
			if l, ok := p.next(); ok && firstWord(l) == "fi" {
				return st, nil
			}
			return nil, fmt.Errorf("syntax error: expected 'fi'")
		default:
			return nil, fmt.Errorf("syntax error near '%s'", kw)
		}
	}
}

func (p *scriptParser) parseFor(line string) (scriptStmt, error) {
	rest := strings.TrimSpace(strings.TrimPrefix(line, "for"))
	fields := strings.Fields(rest)
	if len(fields) < 2 || fields[1] != "in" {
		return nil, fmt.Errorf("syntax error: expected 'for VAR in WORDS'")
	}
	st := forStmt{varName: fields[0]}
	wordsRaw := strings.TrimSpace(strings.TrimPrefix(rest, fields[0]+" in"))
	if strings.HasSuffix(wordsRaw, "; do") {
		st.words = strings.TrimSpace(strings.TrimSuffix(wordsRaw, "; do"))
	} else if strings.HasSuffix(wordsRaw, " do") && !strings.Contains(wordsRaw, ";") {
		// `for x in a b do`-style header; a word merely ending in "do"
		// (e.g. `todo`) must not be trimmed
		st.words = strings.TrimSpace(strings.TrimSuffix(wordsRaw, " do"))
	} else {
		// 'do' on its own line
		for {
			l, ok := p.next()
			if !ok {
				return nil, fmt.Errorf("syntax error: expected 'do'")
			}
			l = strings.TrimSpace(l)
			if l == "do" {
				break
			}
			if strings.HasSuffix(l, "; do") {
				wordsRaw += " " + strings.TrimSpace(strings.TrimSuffix(l, "; do"))
				break
			}
			wordsRaw += " " + l
		}
		st.words = wordsRaw
	}
	body, err := p.parseBlock("for", "done")
	if err != nil {
		return nil, err
	}
	st.body = body
	if l, ok := p.next(); !ok || firstWord(l) != "done" {
		return nil, fmt.Errorf("syntax error: expected 'done'")
	}
	return st, nil
}

func (p *scriptParser) parseWhile(line string) (scriptStmt, error) {
	cond, err := p.condOf(line, "while", "do")
	if err != nil {
		return nil, err
	}
	body, err := p.parseBlock("while", "done")
	if err != nil {
		return nil, err
	}
	st := whileStmt{cond: cond, body: body}
	if l, ok := p.next(); !ok || firstWord(l) != "done" {
		return nil, fmt.Errorf("syntax error: expected 'done'")
	}
	return st, nil
}

// ---- arithmetic ----

// evalArith evaluates $(( )) expressions: + - * / % ( ), unary -,
// comparisons, $VARS and bare names.
func evalArith(expr string, getenv func(string) string) string {
	// expand $vars first
	var b strings.Builder
	for i := 0; i < len(expr); i++ {
		if expr[i] == '$' && i+1 < len(expr) {
			j := i + 1
			for j < len(expr) && (isAlnum(expr[j]) || expr[j] == '_') {
				j++
			}
			if j > i+1 {
				b.WriteString(getenv(expr[i+1 : j]))
				i = j - 1
				continue
			}
		}
		b.WriteByte(expr[i])
	}
	src := strings.ReplaceAll(b.String(), " ", "")
	src = strings.ReplaceAll(src, "\t", "")

	val, pos, err := arithExpr(src, 0, getenv)
	if err != nil || pos != len(src) {
		return "0"
	}
	return strconv.Itoa(int(val))
}

func arithExpr(s string, i int, getenv func(string) string) (float64, int, error) {
	val, i, err := arithCompare(s, i, getenv)
	if err != nil {
		return 0, i, err
	}
	for i < len(s) && (s[i] == '+' || s[i] == '-') {
		op := s[i]
		rhs, ni, err := arithCompare(s, i+1, getenv)
		if err != nil {
			return 0, ni, err
		}
		if op == '+' {
			val += rhs
		} else {
			val -= rhs
		}
		i = ni
	}
	return val, i, nil
}

func arithCompare(s string, i int, getenv func(string) string) (float64, int, error) {
	val, i, err := arithTerm(s, i, getenv)
	if err != nil {
		return 0, i, err
	}
	for i+1 < len(s) {
		var op string
		two := s[i : i+2]
		if two == "<=" || two == ">=" || two == "==" || two == "!=" {
			op = two
		} else if s[i] == '<' || s[i] == '>' {
			op = string(s[i])
		} else {
			break
		}
		rhs, ni, err := arithTerm(s, i+len(op), getenv)
		if err != nil {
			return 0, ni, err
		}
		switch op {
		case "<":
			val = b2f(val < rhs)
		case ">":
			val = b2f(val > rhs)
		case "<=":
			val = b2f(val <= rhs)
		case ">=":
			val = b2f(val >= rhs)
		case "==":
			val = b2f(val == rhs)
		default:
			val = b2f(val != rhs)
		}
		i = ni
	}
	return val, i, nil
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func arithTerm(s string, i int, getenv func(string) string) (float64, int, error) {
	val, i, err := arithFactor(s, i, getenv)
	if err != nil {
		return 0, i, err
	}
	for i < len(s) && (s[i] == '*' || s[i] == '/' || s[i] == '%') {
		op := s[i]
		rhs, ni, err := arithFactor(s, i+1, getenv)
		if err != nil {
			return 0, ni, err
		}
		switch op {
		case '*':
			val *= rhs
		case '/':
			if rhs != 0 {
				val /= rhs
			}
		case '%':
			if rhs != 0 {
				val = float64(int64(val) % int64(rhs))
			}
		}
		i = ni
	}
	return val, i, nil
}

func arithFactor(s string, i int, getenv func(string) string) (float64, int, error) {
	if i >= len(s) {
		return 0, i, fmt.Errorf("unexpected end")
	}
	switch s[i] {
	case '(':
		val, ni, err := arithExpr(s, i+1, getenv)
		if err != nil {
			return 0, ni, err
		}
		if ni >= len(s) || s[ni] != ')' {
			return 0, ni, fmt.Errorf("missing )")
		}
		return val, ni + 1, nil
	case '-':
		val, ni, err := arithFactor(s, i+1, getenv)
		return -val, ni, err
	case '+':
		return arithFactor(s, i+1, getenv)
	}
	j := i
	for j < len(s) && (isAlnum(s[j]) || s[j] == '.') {
		j++
	}
	if j == i {
		return 0, i, fmt.Errorf("unexpected char %q", s[i])
	}
	word := s[i:j]
	if v, err := strconv.ParseFloat(word, 64); err == nil {
		return v, j, nil
	}
	if v, err := strconv.ParseFloat(getenv(word), 64); err == nil {
		return v, j, nil
	}
	return 0, j, nil
}
