package vshell

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func textCmds() []Cmd {
	return []Cmd{
		{
			Name: "sort",
			Man: `sort — отсортировать строки
Синтаксис: sort [-r] [-n] [-u] [-t РАЗД] [-k N] [файл...]
  -r  в обратном порядке
  -n  как числа
  -u  убрать повторы
  -t  разделитель полей
  -k  номер поля для сортировки (с 1)
Пример: sort -t: -k3 -n /etc/passwd`,
			Run: sortRun,
		},
		{
			Name: "uniq",
			Man: `uniq — убрать/посчитать повторы СТОРОЧНО
Синтаксис: uniq [-c] [-d] [-u] [вход [выход]]
  -c  prefixed количеством повторов
  -d  только повторяющиеся строки
  -u  только уникальные строки
Работает по соседним строкам — сначала sort! 
Пример: sort list | uniq -c | sort -rn`,
			Run: uniqRun,
		},
		{
			Name: "cut",
			Man: `cut — вырезать поля/столбцы
Синтаксис: cut -d РАЗД -f СПИСОК | cut -c СПИСОК
  -d  разделитель (по умолчанию TAB)
  -f  номера полей: 2, 1-3, 5-, -2
  -c  номера символов
Примеры: cut -d: -f1 /etc/passwd, cut -c1-10 file`,
			Run: cutRun,
		},
		{
			Name: "tr",
			Man: `tr — заменить или удалить символы
Синтаксис: tr SET1 SET2 | tr -d SET
  -d  удалить символы из SET
Диапазоны: a-z, A-Z, 0-9.
Пример: echo "Hello" | tr a-z A-Z
        cat file | tr -d '\r'`,
			Run: trRun,
		},
		{
			Name: "tee",
			Man: `tee — вывод в файл И на экран
Синтаксис: tee [-a] файл...
  -a  дописывать в конец
Пример: make 2>&1 | tee build.log`,
			Run: teeRun,
		},
		{
			Name: "xargs",
			Man: `xargs — собрать строки в аргументы команды
Синтаксис: команда | xargs [-n N] [команда]
  -n N  по N аргументов за вызов
По умолчанию команда = echo.
Пример: find . -name "*.log" | xargs rm`,
			Run: xargsRun,
		},
	}
}

func sortRun(c *Ctx) int {
	bools, vals, operands, err := parseArgs("rnut:k:", c.Args[1:], map[string]byte{
		"reverse": 'r', "numeric-sort": 'n', "unique": 'u',
		"field-separator": 't', "key": 'k',
	})
	if err != nil {
		return errPlain(c, err.Error())
	}
	inputs, code := collectInputs(c, c.Shell, operands)
	var lines []string
	for _, in := range inputs {
		lines = append(lines, splitLines(in)...)
	}

	sep, hasSep := "\t", false
	if v, ok := vals['t']; ok && v != "" {
		sep, hasSep = v, true
	}

	var keys []sortKey
	if v, ok := vals['k']; ok && v != "" {
		if k := parseKeySpec(v, bools['n'], bools['r']); k != nil {
			keys = append(keys, *k)
		}
	}

	// cmp < 0 when x sorts before y
	cmp := func(x, y string) int {
		if len(keys) > 0 {
			for _, k := range keys {
				ka := keyString(x, sep, hasSep, k)
				kb := keyString(y, sep, hasSep, k)
				r := 0
				if k.numeric {
					an, bn := parseNum(ka), parseNum(kb)
					switch {
					case an < bn:
						r = -1
					case an > bn:
						r = 1
					}
				} else {
					r = strings.Compare(ka, kb)
				}
				if k.reverse {
					r = -r
				}
				if r != 0 {
					return r
				}
			}
			return 0
		}
		if bools['n'] {
			an, bn := parseNum(x), parseNum(y)
			switch {
			case an < bn:
				return -1
			case an > bn:
				return 1
			}
			return 0
		}
		return strings.Compare(x, y)
	}

	sort.SliceStable(lines, func(i, j int) bool { return cmp(lines[i], lines[j]) < 0 })
	if bools['r'] && len(keys) == 0 {
		// plain -r without keys: reversing the ascending order is
		// equivalent and keeps ties in stable order
		for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
			lines[i], lines[j] = lines[j], lines[i]
		}
	}
	if bools['u'] {
		var out []string
		for i, l := range lines {
			if i == 0 || cmp(l, lines[i-1]) != 0 {
				out = append(out, l)
			}
		}
		lines = out
	}
	for _, l := range lines {
		fmt.Fprintln(c.Stdout, l)
	}
	return code
}

// sortKey is one -k specification: F[.C][,F[.C]] plus n/r modifiers.
type sortKey struct {
	start, end   int // field numbers (1-based); end==0 means to end of line
	startC, endC int // char offsets inside the field
	numeric      bool
	reverse      bool
}

func parseKeySpec(spec string, globalN, globalR bool) *sortKey {
	k := &sortKey{numeric: globalN, reverse: globalR}
	main, optB := spec, ""
	if i := strings.IndexByte(spec, ','); i >= 0 {
		main, optB = spec[:i], spec[i+1:]
	}
	if !parseKeyPart(main, k, true) {
		return nil
	}
	if optB != "" && !parseKeyPart(optB, k, false) {
		return nil
	}
	if k.start < 1 {
		return nil
	}
	return k
}

func parseKeyPart(part string, k *sortKey, start bool) bool {
	i := 0
	for i < len(part) && part[i] >= '0' && part[i] <= '9' {
		i++
	}
	if i == 0 {
		return false
	}
	fnum, _ := strconv.Atoi(part[:i])
	if i < len(part) && part[i] == '.' {
		i++
		j := i
		for j < len(part) && part[j] >= '0' && part[j] <= '9' {
			j++
		}
		if j == i {
			return false
		}
		cnum, _ := strconv.Atoi(part[i:j])
		if start {
			k.startC = cnum
		} else {
			k.endC = cnum
		}
		i = j
	}
	for ; i < len(part); i++ {
		switch part[i] {
		case 'n':
			k.numeric = true
		case 'r':
			k.reverse = true
		default:
			return false
		}
	}
	if start {
		k.start = fnum
	} else {
		k.end = fnum
	}
	return true
}

// keyString extracts the key substring: from field k.start (optionally
// k.startC chars in) to end of line, or to the given end field/char.
// Without -t, fields are blank-separated runs with leading blanks skipped,
// like real sort.
func keyString(line, sep string, hasSep bool, k sortKey) string {
	type fieldPos struct{ start, end int }
	var fields []fieldPos
	if hasSep {
		s := 0
		for {
			i := strings.Index(line[s:], sep)
			if i < 0 {
				fields = append(fields, fieldPos{s, len(line)})
				break
			}
			fields = append(fields, fieldPos{s, s + i})
			s += i + len(sep)
		}
	} else {
		i := 0
		for i < len(line) {
			for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
				i++
			}
			if i >= len(line) {
				break
			}
			j := i
			for j < len(line) && line[j] != ' ' && line[j] != '\t' {
				j++
			}
			fields = append(fields, fieldPos{i, j})
			i = j
		}
	}
	if k.start < 1 || k.start > len(fields) {
		return ""
	}
	from := fields[k.start-1].start + k.startC
	if from > len(line) {
		return ""
	}
	to := len(line)
	if k.end > 0 && k.end <= len(fields) {
		to = fields[k.end-1].end - k.endC
		if to < from {
			to = from
		}
	}
	return line[from:to]
}

func parseNum(s string) float64 {
	s = strings.TrimSpace(s)
	prefix := s
	for i, ch := range s {
		if !(ch >= '0' && ch <= '9' || ch == '-' && i == 0 || ch == '+' && i == 0 || ch == '.') {
			prefix = s[:i]
			break
		}
	}
	f, _ := strconv.ParseFloat(strings.TrimSpace(prefix), 64)
	return f
}

func uniqRun(c *Ctx) int {
	s := c.Shell
	bools, _, operands, err := parseArgs("cdu", c.Args[1:], map[string]byte{
		"count": 'c', "repeated": 'd', "unique": 'u',
	})
	if err != nil {
		return errPlain(c, err.Error())
	}
	var input string
	if len(operands) > 0 {
		inputs, code := collectInputs(c, c.Shell, operands[:1])
		if code != 0 {
			return code
		}
		input = inputs[0]
	} else {
		input = c.Stdin
	}
	lines := splitLines(input)
	type entry struct {
		line  string
		count int
	}
	var entries []entry
	for _, l := range lines {
		if len(entries) > 0 && entries[len(entries)-1].line == l {
			entries[len(entries)-1].count++
			continue
		}
		entries = append(entries, entry{l, 1})
	}
	var out strings.Builder
	for _, e := range entries {
		switch {
		case bools['d'] && e.count < 2:
			continue
		case bools['u'] && e.count > 1:
			continue
		}
		if bools['c'] {
			fmt.Fprintf(&out, "%7d %s\n", e.count, e.line)
			continue
		}
		fmt.Fprintln(&out, e.line)
	}
	result := out.String()
	if len(operands) > 1 {
		// `uniq in out` writes the result to the second file
		return s.writeResult(c, operands[1], result)
	}
	fmt.Fprint(c.Stdout, result)
	return 0
}

func cutRun(c *Ctx) int {
	bools, vals, operands, err := parseArgs("d:f:c:s", c.Args[1:], map[string]byte{
		"delimiter": 'd', "fields": 'f', "characters": 'c', "only-delimited": 's',
	})
	if err != nil {
		return errPlain(c, err.Error())
	}
	delim := "\t"
	if v, ok := vals['d']; ok && v != "" {
		delim = unescape(v)
	}
	spec, isFields := vals['f'], true
	if v, ok := vals['c']; ok {
		spec, isFields = v, false
	}
	if spec == "" {
		return errPlain(c, "you must specify a list of bytes, characters, or fields")
	}
	ranges, rerr := parseListRanges(spec)
	if rerr != nil {
		return errPlain(c, "invalid list: "+spec)
	}
	onlyDelimited := bools['s']
	inputs, code := collectInputs(c, c.Shell, operands)
	for _, in := range inputs {
		for _, line := range splitLines(in) {
			if isFields {
				if !strings.Contains(line, delim) {
					// lines without the delimiter are printed in full
					// unless -s suppresses them
					if !onlyDelimited {
						fmt.Fprintln(c.Stdout, line)
					}
					continue
				}
				parts := strings.Split(line, delim)
				var kept []string
				for i, p := range parts {
					if inRanges(ranges, i+1) {
						kept = append(kept, p)
					}
				}
				fmt.Fprintln(c.Stdout, strings.Join(kept, delim))
				continue
			}
			runes := []rune(line)
			var b strings.Builder
			for i, r := range runes {
				if inRanges(ranges, i+1) {
					b.WriteRune(r)
				}
			}
			fmt.Fprintln(c.Stdout, b.String())
		}
	}
	return code
}

// parseListRanges parses "2,4-6,8-,-3" into half-open ranges.
func parseListRanges(spec string) ([][2]int, error) {
	var out [][2]int
	for _, part := range strings.Split(spec, ",") {
		if part == "" {
			return nil, fmt.Errorf("empty")
		}
		if dash := strings.IndexByte(part, '-'); dash >= 0 {
			lo := strings.TrimSpace(part[:dash])
			hi := strings.TrimSpace(part[dash+1:])
			low, high := 1, 1<<30
			if lo != "" {
				v, err := strconv.Atoi(lo)
				if err != nil {
					return nil, err
				}
				low = v
			}
			if hi != "" {
				v, err := strconv.Atoi(hi)
				if err != nil {
					return nil, err
				}
				high = v
			}
			out = append(out, [2]int{low, high})
			continue
		}
		v, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		out = append(out, [2]int{v, v})
	}
	return out, nil
}

func inRanges(ranges [][2]int, n int) bool {
	for _, r := range ranges {
		if n >= r[0] && n <= r[1] {
			return true
		}
	}
	return false
}

func trRun(c *Ctx) int {
	bools, _, operands, err := parseArgs("d", c.Args[1:], map[string]byte{"delete": 'd'})
	if err != nil {
		return errPlain(c, err.Error())
	}
	if bools['d'] {
		if len(operands) == 0 {
			return errPlain(c, "missing operand after '-d'")
		}
		del := expandSet(unescape(operands[0]))
		out := strings.Map(func(r rune) rune {
			if strings.ContainsRune(del, r) {
				return -1
			}
			return r
		}, c.Stdin)
		fmt.Fprint(c.Stdout, out)
		return 0
	}
	if len(operands) < 2 {
		return errPlain(c, "missing operand")
	}
	set1 := expandSet(unescape(operands[0]))
	set2 := expandSet(unescape(operands[1]))
	runes1 := []rune(set1)
	runes2 := []rune(set2)
	out := strings.Map(func(r rune) rune {
		for i, r1 := range runes1 {
			if r1 == r {
				if i >= len(runes2) {
					// POSIX: excess SET1 chars map to the last of SET2
					return runes2[len(runes2)-1]
				}
				return runes2[i]
			}
		}
		return r
	}, c.Stdin)
	fmt.Fprint(c.Stdout, out)
	return 0
}

// expandSet turns "a-z" style sets into full character sets.
func expandSet(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if i+2 < len(s)+1 && i+2 <= len(s)-1 && s[i+1] == '-' {
			// range like a-z
			if s[i] < s[i+2] {
				for ch := s[i]; ch <= s[i+2]; ch++ {
					b.WriteByte(ch)
				}
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// writeResult writes command output to a file operand (used by uniq).
func (s *Shell) writeResult(c *Ctx, path, content string) int {
	if !s.redirectAllowed(path) {
		fmt.Fprintf(c.Stderr, "%s: %s: Permission denied\n", c.Args[0], path)
		return 1
	}
	if err := s.FS.WriteFile(s.Cwd, path, content, 0o644&^s.Umask, s.User, s.User); err != nil {
		return errNode(c, path, err)
	}
	return 0
}

func teeRun(c *Ctx) int {
	bools, _, operands, err := parseArgs("a", c.Args[1:], map[string]byte{"append": 'a'})
	if err != nil {
		return errPlain(c, err.Error())
	}
	s := c.Shell
	for _, p := range operands {
		if !s.redirectAllowed(p) {
			fmt.Fprintf(c.Stderr, "tee: %s: Permission denied\n", p)
			continue
		}
		var werr error
		if bools['a'] {
			werr = s.FS.AppendFile(s.Cwd, p, c.Stdin, 0o644&^s.Umask, s.User, s.User)
		} else {
			werr = s.FS.WriteFile(s.Cwd, p, c.Stdin, 0o644&^s.Umask, s.User, s.User)
		}
		if werr != nil {
			return errNode(c, p, werr)
		}
	}
	fmt.Fprint(c.Stdout, c.Stdin)
	return 0
}

func xargsRun(c *Ctx) int {
	bools, vals, rest, err := parseArgs("n:r", c.Args[1:], map[string]byte{
		"max-args": 'n', "no-run-if-empty": 'r',
	})
	if err != nil {
		return errPlain(c, err.Error())
	}
	perCall := 1 << 30
	if v, ok := vals['n']; ok {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 1 {
			return errPlain(c, "invalid value for '-n': "+v)
		}
		perCall = n
	}
	command := []string{"echo"}
	if len(rest) > 0 {
		command = rest
	}

	// split stdin into words
	var words []string
	for _, line := range splitLines(c.Stdin) {
		words = append(words, strings.Fields(line)...)
	}
	if len(words) == 0 && bools['r'] {
		return 0 // no-run-if-empty
	}
	if len(words) == 0 && len(rest) > 0 {
		// run command once with no extra args
		return c.Shell.runCaptured(command, c)
	}

	code := 0
	for start := 0; start < len(words); start += perCall {
		end := start + perCall
		if end > len(words) {
			end = len(words)
		}
		args := append(append([]string{}, command...), words[start:end]...)
		if ccode := c.Shell.runCaptured(args, c); ccode != 0 {
			code = ccode
		}
	}
	return code
}

// runCaptured executes args without recording history, writing into c.
// Args are quoted so that spaces and glob characters in file names
// survive the re-parse.
func (s *Shell) runCaptured(args []string, c *Ctx) int {
	if len(args) == 0 {
		return 0
	}
	suppress := s.suppressHistory
	s.suppressHistory = true
	defer func() { s.suppressHistory = suppress }()

	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = strconv.Quote(a)
	}
	line := strings.Join(quoted, " ")
	return s.execCommandLine(line, c.Stdout, c.Stderr, &Result{})
}
