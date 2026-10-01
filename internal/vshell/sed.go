package vshell

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// sedAddr is a sed command address.
type sedAddr struct {
	kind  byte // 0 none, 'n' line number, '$' last, '/' regex
	num   int
	re    *regexp.Regexp
	reSrc string
}

type sedRule struct {
	addr1, addr2 sedAddr
	hasAddr2     bool
	cmd          byte // 's', 'd', 'p'
	pat          *regexp.Regexp
	patSrc       string
	repl         string
	global       bool
	icase        bool
	nth          int  // substitute Nth occurrence
	print        bool // p flag on s///
}

func sedCmds() []Cmd {
	return []Cmd{
		{
			Name: "sed",
			Man: `sed — потоковый редактор
Синтаксис: sed [-n] [-i] 'КОМАНДЫ' [файл...]
Замена: s/ШАБЛОН/ЗАМЕНА/флаги
  g  все вхождения, i  без учёта регистра, p  печатать
Удаление: /ШАБЛОН/d, 5d, $d, 2,5d
Адресация: N-я строка, $ последняя, /ре/
  -n  печатать только по команде p
  -i  изменить файл на месте
Примеры: sed 's/cat/dog/g' file
         sed -n '/ERROR/p' log
         sed -i '3d' list.txt`,
			Run: sedRun,
		},
	}
}

func sedRun(c *Ctx) int {
	s := c.Shell
	bools, vals, rest, err := parseArgs("niEe:", c.Args[1:], map[string]byte{
		"quiet": 'n', "in-place": 'i', "regexp-extended": 'E', "expression": 'e',
	})
	if err != nil {
		return errPlain(c, err.Error())
	}
	program := vals['e']
	if program == "" && len(rest) > 0 {
		program = rest[0]
		rest = rest[1:]
	}
	if program == "" {
		return errPlain(c, "no script given")
	}
	rules, perr := parseSedProgram(program, bools['E'])
	if perr != nil {
		return errPlain(c, perr.Error())
	}

	// -i processes and rewrites each file separately; without -i all
	// inputs form one continuous stream, like real sed
	if bools['i'] {
		if len(rest) == 0 {
			return errPlain(c, "-i may not be used with stdin")
		}
		code := 0
		for _, p := range rest {
			n, lerr := s.FS.Lookup(s.Cwd, p)
			if lerr != nil {
				code = errNode(c, p, lerr)
				continue
			}
			if n.Type == TypeDir {
				code = errNode(c, p, ErrIsDir)
				continue
			}
			if !CanRead(n, s.User) || !CanWrite(n, s.User) {
				fmt.Fprintf(c.Stderr, "sed: %s: Permission denied\n", p)
				code = 2
				continue
			}
			lines := splitLines(n.Content)
			out := runSedRules(rules, lines, bools['n'])
			result := joinSedOut(out)
			if werr := s.FS.WriteFile(s.Cwd, p, result, n.Perms, n.Owner, n.Group); werr != nil {
				code = errNode(c, p, werr)
			}
		}
		return code
	}

	var allLines []string
	code := 0
	if len(rest) == 0 {
		allLines = splitLines(c.Stdin)
	} else {
		for _, p := range rest {
			n, lerr := s.FS.Lookup(s.Cwd, p)
			if lerr != nil {
				code = errNode(c, p, lerr)
				continue
			}
			if n.Type == TypeDir {
				code = errNode(c, p, ErrIsDir)
				continue
			}
			if !CanRead(n, s.User) {
				fmt.Fprintf(c.Stderr, "sed: %s: Permission denied\n", p)
				code = 2
				continue
			}
			allLines = append(allLines, splitLines(n.Content)...)
		}
	}
	out := runSedRules(rules, allLines, bools['n'])
	fmt.Fprint(c.Stdout, joinSedOut(out))
	return code
}

// joinSedOut joins processed lines back into a text block.
func joinSedOut(out []string) string {
	result := strings.Join(out, "\n")
	if len(out) > 0 {
		result += "\n"
	}
	return result
}

// parseSedProgram parses the supported sed subset. When extended is set
// (-E) patterns are already ERE and skip BRE translation.
func parseSedProgram(src string, extended bool) ([]sedRule, error) {
	var rules []sedRule
	i := 0
	skip := func() {
		for i < len(src) && (src[i] == ' ' || src[i] == '\t' || src[i] == ';' || src[i] == '\n') {
			i++
		}
	}
	readDelim := func(delim byte) (string, int, error) {
		var b strings.Builder
		for j := i; j < len(src); j++ {
			if src[j] == '\\' && j+1 < len(src) {
				if src[j+1] == delim {
					b.WriteByte(delim)
					j++
					continue
				}
				if src[j+1] == '\\' {
					b.WriteByte('\\')
					j++
					continue
				}
				if src[j+1] == 'n' {
					b.WriteByte('\n')
					j++
					continue
				}
				if src[j+1] == 't' {
					b.WriteByte('\t')
					j++
					continue
				}
				b.WriteByte(src[j])
				b.WriteByte(src[j+1])
				j++
				continue
			}
			if src[j] == delim {
				return b.String(), j + 1 - i, nil
			}
			b.WriteByte(src[j])
		}
		return "", 0, fmt.Errorf("unterminated expression")
	}

	for {
		skip()
		if i >= len(src) {
			break
		}
		rule := sedRule{nth: 0}
		// addresses
		parseAddr := func() (sedAddr, error) {
			a := sedAddr{}
			switch src[i] {
			case '$':
				a.kind = '$'
				i++
			case '/':
				i++
				pat, n, err := readDelim('/')
				if err != nil {
					return a, err
				}
				i += n
				addrPat := pat
				if !extended {
					addrPat = breToRE(pat)
				}
				re, cerr := regexp.Compile(addrPat)
				if cerr != nil {
					return a, fmt.Errorf("bad regex: %s", pat)
				}
				a.kind, a.re, a.reSrc = '/', re, pat
			default:
				j := i
				for j < len(src) && src[j] >= '0' && src[j] <= '9' {
					j++
				}
				if j == i {
					return a, nil // no address
				}
				n, _ := strconv.Atoi(src[i:j])
				a.kind, a.num = 'n', n
				i = j
			}
			return a, nil
		}
		var aerr error
		if rule.addr1, aerr = parseAddr(); aerr != nil {
			return nil, aerr
		}
		skip()
		if i < len(src) && src[i] == ',' {
			i++
			skip()
			if rule.addr2, aerr = parseAddr(); aerr != nil {
				return nil, aerr
			}
			rule.hasAddr2 = true
		}
		skip()
		if i >= len(src) {
			return nil, fmt.Errorf("missing command")
		}
		switch src[i] {
		case 's':
			i++
			if i >= len(src) {
				return nil, fmt.Errorf("missing s delimiter")
			}
			delim := src[i]
			if delim == '\\' {
				return nil, fmt.Errorf("bad delimiter")
			}
			i++
			pat, n, err := readDelim(delim)
			if err != nil {
				return nil, err
			}
			i += n
			repl, n, err := readDelim(delim)
			if err != nil {
				return nil, err
			}
			i += n
			rule.cmd = 's'
			rule.repl = repl
			rule.patSrc = pat
			// flags
			nthGiven := false
			for i < len(src) && strings.IndexByte("gip0123456789", src[i]) >= 0 {
				switch {
				case src[i] == 'g':
					rule.global = true
				case src[i] == 'i':
					rule.icase = true
				case src[i] == 'p':
					rule.print = true
				default:
					rule.nth = rule.nth*10 + int(src[i]-'0')
					nthGiven = true
				}
				i++
			}
			if nthGiven && rule.nth == 0 {
				return nil, fmt.Errorf("number option to `s' command may not be zero")
			}
			pattern := pat
			if !extended {
				pattern = breToRE(pat)
			}
			if rule.icase {
				pattern = "(?i)" + pattern
			}
			re, cerr := regexp.Compile(pattern)
			if cerr != nil {
				return nil, fmt.Errorf("bad regex: %s", pat)
			}
			rule.pat = re
		case 'd', 'p':
			rule.cmd = src[i]
			i++
		default:
			return nil, fmt.Errorf("unsupported command '%c' (тренажёр поддерживает s, d, p)", src[i])
		}
		rules = append(rules, rule)
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("empty program")
	}
	return rules, nil
}

// runSedRules applies the rules to lines; quiet = -n.
func runSedRules(rules []sedRule, lines []string, quiet bool) []string {
	var out []string
	inRange := make([]bool, len(rules))

	matchAddr := func(a sedAddr, nr, total int, line string) bool {
		switch a.kind {
		case 0:
			return false
		case 'n':
			return a.num == nr
		case '$':
			return nr == total
		case '/':
			return a.re.MatchString(line)
		}
		return false
	}

	for nr := 1; nr <= len(lines); nr++ {
		line := lines[nr-1]
		deleted := false
		buf := line

		for ri, rule := range rules {
			active := false
			switch {
			case rule.addr1.kind == 0:
				active = true
			case rule.hasAddr2:
				if inRange[ri] {
					active = true
					if matchAddr(rule.addr2, nr, len(lines), line) {
						inRange[ri] = false
					}
				} else if matchAddr(rule.addr1, nr, len(lines), line) {
					active = true
					// numeric addr2 <= current line means a one-line range
					if rule.addr2.kind == 'n' && rule.addr2.num > nr || rule.addr2.kind != 'n' {
						inRange[ri] = true
					}
				}
			default:
				active = matchAddr(rule.addr1, nr, len(lines), line)
			}
			if !active {
				continue
			}
			switch rule.cmd {
			case 'd':
				deleted = true
			case 'p':
				out = append(out, buf)
			case 's':
				newLine, changed := sedSubstitute(buf, rule)
				buf = newLine
				if changed && rule.print {
					out = append(out, buf)
				}
			}
			if deleted {
				break
			}
		}
		if deleted {
			continue
		}
		if !quiet {
			out = append(out, buf)
		}
	}
	return out
}

// sedSubstitute performs the s/pat/repl/ substitution on one line.
func sedSubstitute(line string, rule sedRule) (string, bool) {
	re := rule.pat
	matches := re.FindAllStringSubmatchIndex(line, -1)
	if len(matches) == 0 {
		return line, false
	}
	// sed replacement → Go ReplaceAll template
	tmpl := sedReplToTemplate(rule.repl)

	useAll := rule.global
	if useAll {
		out := re.ReplaceAllString(line, tmpl)
		return out, out != line
	}
	// replace only the Nth occurrence (N defaults to the first)
	n := rule.nth
	if n == 0 {
		n = 1
	}
	if n > len(matches) {
		return line, false
	}
	m := matches[n-1]
	var b strings.Builder
	b.WriteString(line[:m[0]])
	b.WriteString(expandTemplate(re, line, m, tmpl))
	b.WriteString(line[m[1]:])
	return b.String(), true
}

// sedReplToTemplate converts a sed replacement (& and \N) into a Go template.
func sedReplToTemplate(repl string) string {
	var b strings.Builder
	for i := 0; i < len(repl); i++ {
		switch repl[i] {
		case '&':
			b.WriteString("${0}")
		case '\\':
			if i+1 < len(repl) && repl[i+1] >= '0' && repl[i+1] <= '9' {
				b.WriteString("${")
				b.WriteByte(repl[i+1])
				b.WriteString("}")
				i++
				continue
			}
			if i+1 < len(repl) && repl[i+1] == '&' {
				b.WriteByte('&')
				i++
				continue
			}
			if i+1 < len(repl) && repl[i+1] == '\\' {
				b.WriteByte('\\')
				i++
				continue
			}
			b.WriteByte('\\')
		case '$':
			b.WriteString("$$")
		default:
			b.WriteByte(repl[i])
		}
	}
	return b.String()
}

// expandTemplate expands ${N} in tmpl against one match.
func expandTemplate(re *regexp.Regexp, line string, m []int, tmpl string) string {
	var b strings.Builder
	for i := 0; i < len(tmpl); i++ {
		if tmpl[i] != '$' || i+1 >= len(tmpl) {
			b.WriteByte(tmpl[i])
			continue
		}
		if tmpl[i+1] == '$' {
			b.WriteByte('$')
			i++
			continue
		}
		if tmpl[i+1] == '{' && i+2 < len(tmpl) {
			end := strings.IndexByte(tmpl[i+2:], '}')
			if end < 0 {
				b.WriteByte(tmpl[i])
				continue
			}
			idx, _ := strconv.Atoi(tmpl[i+2 : i+2+end])
			if idx == 0 {
				b.WriteString(line[m[0]:m[1]])
			} else if 2*idx+1 < len(m) && m[2*idx] >= 0 {
				b.WriteString(line[m[2*idx]:m[2*idx+1]])
			}
			i += 2 + end
			continue
		}
		b.WriteByte(tmpl[i])
	}
	return b.String()
}
