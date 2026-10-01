package vshell

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

func searchCmds() []Cmd {
	return []Cmd{
		{
			Name: "find",
			Man: `find — поиск файлов по критериям
Синтаксис: find [путь] [выражение]
  -name ШАБЛОН   имя (можно * и ?)
  -type f|d|l    тип: файл/каталог/ссылка
  -size +N[kMG]  больше N, -N — меньше
  -mtime +N/-N/N  старше N дней / свежее N дней / ровно N дней
  -exec КОМД {} ;  выполнить команду для каждого
Пример: find . -name "*.txt" -type f`,
			Run: findRun,
		},
		{
			Name: "grep",
			Man: `grep — поиск строк по шаблону
Синтаксис: grep [-i] [-v] [-n] [-r] [-c] [-l] [-E] ШАБЛОН [файл...]
  -i  игнорировать регистр
  -v  инвертировать (строки БЕЗ шаблона)
  -n  печатать номера строк
  -r  рекурсивно по каталогу
  -c  только количество совпадений
  -l  только имена файлов с совпадениями
  -E  расширенные регулярные выражения ( + | () {} )
Выход: 0 — есть совпадения, 1 — нет.`,
			Run: grepRun,
		},
	}
}

func findRun(c *Ctx) int {
	s := c.Shell
	operands := c.Args[1:]
	start := "."
	if len(operands) > 0 && !strings.HasPrefix(operands[0], "-") {
		start = operands[0]
		operands = operands[1:]
	}

	// parse the expression: -name X, -type X, -size X, -mtime N, -exec ... ;
	var namePat, ftype string
	var mtimeSet bool
	var mtimeVal, mtimeSign int // sign: 0 = exactly N, +1 = older (+N), -1 = newer (-N)
	sizeSet := false
	var sizeVal, sizeMult int64
	var sizeMode byte // '+', '-' or '='
	var execParts []string
	execPlus := false
	inExec := false
	i := 0
	for i < len(operands) {
		op := operands[i]
		switch {
		case inExec:
			if op == ";" || op == "+" {
				inExec = false
				execPlus = op == "+"
				i++
				continue
			}
			execParts = append(execParts, op)
			i++
		case op == "-name" && i+1 < len(operands):
			namePat = operands[i+1]
			i += 2
		case op == "-type" && i+1 < len(operands):
			ftype = operands[i+1]
			i += 2
		case op == "-size" && i+1 < len(operands):
			v, mult, mode, serr := parseSizeSpec(operands[i+1])
			if serr != nil {
				return errPlain(c, "invalid argument '"+operands[i+1]+"' to '-size'")
			}
			sizeSet, sizeVal, sizeMult, sizeMode = true, v, mult, mode
			i += 2
		case op == "-mtime" && i+1 < len(operands):
			spec := operands[i+1]
			switch {
			case strings.HasPrefix(spec, "+"):
				mtimeSign, spec = 1, spec[1:]
			case strings.HasPrefix(spec, "-"):
				mtimeSign, spec = -1, spec[1:]
			}
			d, perr := strconv.Atoi(spec)
			if perr != nil {
				return errPlain(c, "invalid argument '"+operands[i+1]+"' to '-mtime'")
			}
			mtimeSet, mtimeVal = true, d
			i += 2
		case op == "-exec":
			inExec = true
			i++
		default:
			return errPlain(c, "unknown predicate '"+op+"'")
		}
	}
	if inExec {
		return errPlain(c, "missing argument to '-exec'")
	}

	startNode, lerr := s.FS.Lookup(s.Cwd, start)
	if lerr != nil {
		return errNode(c, start, lerr)
	}
	_ = startNode

	var found []string
	werr := s.FS.Walk(s.Cwd, start, func(abs string, n *Node) error {
		if namePat != "" && !matchSegment(namePat, n.Name) {
			return nil
		}
		if ftype != "" {
			switch ftype {
			case "f":
				if n.Type != TypeFile {
					return nil
				}
			case "d":
				if n.Type != TypeDir {
					return nil
				}
			case "l":
				if n.Type != TypeSymlink {
					return nil
				}
			}
		}
		if sizeSet {
			units := (int64(n.Size()) + sizeMult - 1) / sizeMult // round up to whole units
			switch sizeMode {
			case '+':
				if units <= sizeVal {
					return nil
				}
			case '-':
				if units >= sizeVal {
					return nil
				}
			default:
				if units != sizeVal {
					return nil
				}
			}
		}
		if mtimeSet {
			// find(1) truncates the age to whole days before comparing
			ageDays := int(s.FS.Now().Sub(n.MTime).Hours() / 24)
			switch {
			case mtimeSign > 0 && ageDays <= mtimeVal,
				mtimeSign < 0 && ageDays >= mtimeVal,
				mtimeSign == 0 && ageDays != mtimeVal:
				return nil
			}
		}
		display := displayPath(s, start, abs)
		if abs == s.Cwd && !strings.HasPrefix(start, "/") {
			display = start
		}
		found = append(found, display)
		return nil
	})
	if werr != nil {
		return errNode(c, start, werr)
	}

	if len(execParts) > 0 {
		code := 0
		if execPlus {
			// -exec ... {} + : all names in one invocation
			argv := execArgv(execParts, found)
			if len(argv) > 0 {
				code = s.runCaptured(argv, c)
			}
			return code
		}
		for _, f := range found {
			if ccode := s.runCaptured(execArgv(execParts, []string{f}), c); ccode != 0 {
				code = ccode
			}
		}
		return code
	}
	for _, f := range found {
		fmt.Fprintln(c.Stdout, f)
	}
	return 0
}

// parseSizeSpec parses -size: [+|-]N[c|b|w|k|M|G]. The default unit is
// a 512-byte block, like in POSIX find.
func parseSizeSpec(spec string) (int64, int64, byte, error) {
	mode := byte('=')
	switch {
	case strings.HasPrefix(spec, "+"):
		mode, spec = '+', spec[1:]
	case strings.HasPrefix(spec, "-"):
		mode, spec = '-', spec[1:]
	}
	mult := int64(512)
	if len(spec) > 0 {
		switch spec[len(spec)-1] {
		case 'c':
			mult, spec = 1, spec[:len(spec)-1]
		case 'b':
			mult, spec = 512, spec[:len(spec)-1]
		case 'w':
			mult, spec = 2, spec[:len(spec)-1]
		case 'k':
			mult, spec = 1024, spec[:len(spec)-1]
		case 'M':
			mult, spec = 1024*1024, spec[:len(spec)-1]
		case 'G':
			mult, spec = 1024*1024*1024, spec[:len(spec)-1]
		}
	}
	v, err := strconv.ParseInt(spec, 10, 64)
	if err != nil || v < 0 {
		return 0, 0, 0, fmt.Errorf("bad size")
	}
	return v, mult, mode, nil
}

// execArgv substitutes {} in -exec parts (also inside larger arguments).
// With multiple files ({} + form) a bare {} expands to all of them.
func execArgv(parts []string, files []string) []string {
	var argv []string
	for _, p := range parts {
		if p == "{}" {
			argv = append(argv, files...)
			continue
		}
		if strings.Contains(p, "{}") {
			if len(files) == 1 {
				argv = append(argv, strings.ReplaceAll(p, "{}", files[0]))
			} else {
				argv = append(argv, strings.ReplaceAll(p, "{}", strings.Join(files, " ")))
			}
			continue
		}
		argv = append(argv, p)
	}
	return argv
}

func grepRun(c *Ctx) int {
	s := c.Shell
	bools, _, operands, perr := parseArgs("ivnrclEFwq", c.Args[1:], map[string]byte{
		"ignore-case": 'i', "invert-match": 'v', "line-number": 'n',
		"recursive": 'r', "count": 'c', "files-with-matches": 'l',
		"extended-regexp": 'E', "fixed-strings": 'F', "word-regexp": 'w', "quiet": 'q',
	})
	if perr != nil {
		return errPlain(c, perr.Error())
	}
	if len(operands) == 0 {
		return errPlain(c, "missing pattern")
	}
	pattern := operands[0]
	files := operands[1:]
	if len(files) == 0 && !bools['r'] {
		files = []string{"-"}
	}

	re, err := compilePattern(pattern, bools['F'], bools['i'], bools['E'])
	if err != nil {
		return errPlain(c, "invalid pattern: "+err.Error())
	}
	if bools['w'] {
		re = regexp.MustCompile(`\b(?:` + re.String() + `)\b`)
	}

	type match struct {
		file     string
		line     string
		lineNo   int
		noPrefix bool // directly named single file operand
	}
	var matches []match
	directFile := len(files) == 1 && files[0] != "-"

	addMatches := func(name, content string, noPrefix bool) {
		for i, line := range splitLines(content) {
			hit := re.MatchString(line)
			if bools['v'] {
				hit = !hit
			}
			if hit {
				matches = append(matches, match{name, line, i + 1, noPrefix})
			}
		}
	}

	code := 0
	if bools['r'] {
		roots := files
		if len(roots) == 0 {
			roots = []string{"."}
		}
		for _, root := range roots {
			n, lerr := s.FS.Lookup(s.Cwd, root)
			if lerr != nil {
				code = 2
				fmt.Fprintf(c.Stderr, "%s: %s: %s\n", c.Args[0], root, lerr)
				continue
			}
			if n.Type != TypeDir {
				if n.Type == TypeFile && !CanRead(n, s.User) {
					fmt.Fprintf(c.Stderr, "%s: %s: Permission denied\n", c.Args[0], root)
					code = 2
					continue
				}
				addMatches(root, n.Content, directFile)
				continue
			}
			_ = s.FS.Walk(s.Cwd, root, func(abs string, fn *Node) error {
				if fn.Type == TypeFile && CanRead(fn, s.User) {
					display := displayPath(s, root, abs)
					addMatches(display, fn.Content, false)
				}
				return nil
			})
		}
	} else {
		for _, f := range files {
			if f == "-" {
				addMatches("(stdin)", c.Stdin, true)
				continue
			}
			n, lerr := s.FS.Lookup(s.Cwd, f)
			if lerr != nil {
				code = 2 // grep exits 2 on errors, 1 only for "no matches"
				fmt.Fprintf(c.Stderr, "%s: %s: %s\n", c.Args[0], f, lerr)
				continue
			}
			if n.Type == TypeDir {
				code = 2
				fmt.Fprintf(c.Stderr, "%s: %s: %s\n", c.Args[0], f, ErrIsDir)
				continue
			}
			if !CanRead(n, s.User) {
				fmt.Fprintf(c.Stderr, "%s: %s: Permission denied\n", c.Args[0], f)
				code = 2
				continue
			}
			addMatches(f, n.Content, directFile)
		}
	}

	if bools['q'] {
		if len(matches) == 0 {
			return 1
		}
		return 0
	}

	switch {
	case bools['c']:
		if bools['r'] || len(files) > 1 && files[0] != "-" {
			counts := map[string]int{}
			order := []string{}
			for _, m := range matches {
				if _, ok := counts[m.file]; !ok {
					order = append(order, m.file)
				}
				counts[m.file]++
			}
			for _, f := range order {
				fmt.Fprintf(c.Stdout, "%s:%d\n", f, counts[f])
			}
		} else {
			fmt.Fprintf(c.Stdout, "%d\n", len(matches))
		}
	case bools['l']:
		seen := map[string]bool{}
		for _, m := range matches {
			if !seen[m.file] {
				seen[m.file] = true
				fmt.Fprintln(c.Stdout, m.file)
			}
		}
	default:
		for _, m := range matches {
			if m.file == "(stdin)" {
				if bools['n'] {
					fmt.Fprintf(c.Stdout, "%d:%s\n", m.lineNo, m.line)
				} else {
					fmt.Fprintln(c.Stdout, m.line)
				}
				continue
			}
			prefix := ""
			if (bools['r'] || len(files) > 1) && !m.noPrefix {
				prefix = m.file + ":"
			}
			if bools['n'] {
				prefix += strconv.Itoa(m.lineNo) + ":"
			}
			fmt.Fprintf(c.Stdout, "%s%s\n", prefix, m.line)
		}
	}

	if code != 0 {
		return code // read errors take precedence over "no matches"
	}
	if len(matches) == 0 {
		return 1
	}
	return 0
}

// displayPath renders abs relative to the traversal root when the root
// itself was relative.
func displayPath(s *Shell, root, abs string) string {
	if strings.HasPrefix(root, "/") {
		return abs
	}
	if root == "." {
		if abs == s.Cwd {
			return "."
		}
		return "./" + strings.TrimPrefix(abs, s.Cwd+"/")
	}
	return strings.TrimPrefix(abs, s.Cwd+"/")
}

func compilePattern(pattern string, fixed, ignoreCase, extended bool) (*regexp.Regexp, error) {
	if fixed {
		pattern = regexp.QuoteMeta(pattern)
	} else if !extended {
		// basic regex: \( \) \{ \} are operators and bare + ? | are
		// literals — the opposite of RE2
		pattern = breToRE(pattern)
	}
	if ignoreCase {
		pattern = "(?i)" + pattern
	}
	return regexp.Compile(pattern)
}
