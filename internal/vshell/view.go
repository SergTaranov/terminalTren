package vshell

import (
	"fmt"
	"strconv"
	"strings"
)

func viewCmds() []Cmd {
	return []Cmd{
		{
			Name: "cat",
			Man: `cat — вывести содержимое файлов
Синтаксис: cat [-n] [файл...]
  -n  нумеровать строки
Без файла читает стандартный ввод (конвейер).
cat file1 file2 > all — склейка файлов.`,
			Run: func(c *Ctx) int {
				s := c.Shell
				bools, _, operands, err := parseArgs("n", c.Args[1:], map[string]byte{"number": 'n'})
				if err != nil {
					return errPlain(c, err.Error())
				}
				return catInput(c, s, bools['n'], operands)
			},
		},
		{
			Name: "less",
			Man: `less — постраничный просмотр файла
Синтаксис: less файл
В тренажёре работает как cat (просмотр целиком).`,
			Run: func(c *Ctx) int {
				_, _, operands, err := parseArgs("", c.Args[1:], nil)
				if err != nil {
					return errPlain(c, err.Error())
				}
				return catInput(c, c.Shell, false, operands)
			},
		},
		{
			Name: "nl",
			Man: `nl — нумеровать строки файла
Синтаксис: nl файл`,
			Run: func(c *Ctx) int {
				_, _, operands, err := parseArgs("", c.Args[1:], nil)
				if err != nil {
					return errPlain(c, err.Error())
				}
				return catInput(c, c.Shell, true, operands)
			},
		},
		{
			Name: "head",
			Man: `head — первые строки файла
Синтаксис: head [-n N] [файл...]
По умолчанию 10 строк. -n N — ровно N строк.
head -n 20 file | tail -n 1 — вывести 20-ю строку.`,
			Run: func(c *Ctx) int { return headTail(c, false) },
		},
		{
			Name: "tail",
			Man: `tail — последние строки файла
Синтаксис: tail [-n N] [файл...]
По умолчанию 10 строк.
tail -f лог — следить за файлом (в тренажёре = tail).`,
			Run: func(c *Ctx) int { return headTail(c, true) },
		},
		{
			Name: "wc",
			Man: `wc — посчитать строки, слова, байты
Синтаксис: wc [-l] [-w] [-c] [файл...]
  -l  только строки
  -w  только слова
  -c  только байты
Например: grep -c ERROR log — аналогично grep ERROR log | wc -l.`,
			Run: func(c *Ctx) int {
				bools, _, operands, err := parseArgs("lwc", c.Args[1:], map[string]byte{
					"lines": 'l', "words": 'w', "chars": 'c', "bytes": 'c',
				})
				if err != nil {
					return errPlain(c, err.Error())
				}
				type wcCount struct{ lines, words, chars int }
				count := func(in string) wcCount {
					return wcCount{countLines(in), len(strings.Fields(in)), len(in)}
				}
				printRow := func(cnt wcCount, suffix string) {
					switch {
					case bools['l'] && !bools['w'] && !bools['c']:
						fmt.Fprintf(c.Stdout, "%d%s\n", cnt.lines, suffix)
					case bools['w'] && !bools['l'] && !bools['c']:
						fmt.Fprintf(c.Stdout, "%d%s\n", cnt.words, suffix)
					case bools['c'] && !bools['l'] && !bools['w']:
						fmt.Fprintf(c.Stdout, "%d%s\n", cnt.chars, suffix)
					default:
						fmt.Fprintf(c.Stdout, "%7d %7d %7d%s\n", cnt.lines, cnt.words, cnt.chars, suffix)
					}
				}
				if len(operands) == 0 {
					operands = []string{"-"}
				}
				s := c.Shell
				code := 0
				var total wcCount
				named := 0
				for _, p := range operands {
					content, rcode := readOneOperand(c, s, p)
					if rcode != 0 {
						code = rcode
						continue
					}
					cnt := count(content)
					total.lines += cnt.lines
					total.words += cnt.words
					total.chars += cnt.chars
					suffix := ""
					if p != "-" {
						suffix = " " + p
						named++
					}
					printRow(cnt, suffix)
				}
				if named > 1 {
					printRow(total, " total")
				}
				return code
			},
		},
	}
}

// collectInputs reads files (or stdin) for text-processing commands.
func collectInputs(c *Ctx, s *Shell, operands []string) ([]string, int) {
	if len(operands) == 0 {
		return []string{c.Stdin}, 0
	}
	var out []string
	code := 0
	for _, p := range operands {
		n, err := s.FS.Lookup(s.Cwd, p)
		if err != nil {
			code = errNode(c, p, err)
			continue
		}
		if !CanRead(n, s.User) {
			fmt.Fprintf(c.Stderr, "%s: %s: Permission denied\n", c.Args[0], p)
			code = 1
			continue
		}
		if n.Type == TypeDir {
			code = errNode(c, p, ErrIsDir)
			continue
		}
		out = append(out, n.Content)
	}
	if len(out) == 0 && code == 0 {
		out = []string{c.Stdin}
	}
	return out, code
}

func catInput(c *Ctx, s *Shell, number bool, operands []string) int {
	inputs, code := collectInputs(c, s, operands)
	if number {
		// numbering continues across files, separator is a TAB (GNU cat/nl)
		lineNo := 0
		for _, in := range inputs {
			for _, line := range splitLines(in) {
				lineNo++
				fmt.Fprintf(c.Stdout, "%6d\t%s\n", lineNo, line)
			}
		}
		return code
	}
	for _, in := range inputs {
		fmt.Fprint(c.Stdout, in)
		if in != "" && !strings.HasSuffix(in, "\n") {
			fmt.Fprintln(c.Stdout)
		}
	}
	return code
}

// readOneOperand reads one file operand ("-" means stdin).
func readOneOperand(c *Ctx, s *Shell, p string) (string, int) {
	if p == "-" {
		return c.Stdin, 0
	}
	n, err := s.FS.Lookup(s.Cwd, p)
	if err != nil {
		return "", errNode(c, p, err)
	}
	if n.Type == TypeDir {
		return "", errNode(c, p, ErrIsDir)
	}
	if !CanRead(n, s.User) {
		fmt.Fprintf(c.Stderr, "%s: %s: Permission denied\n", c.Args[0], p)
		return "", 1
	}
	return n.Content, 0
}

func headTail(c *Ctx, tail bool) int {
	s := c.Shell
	_, vals, operands, err := parseArgs("n:", expandOldCount(c.Args[1:]), map[string]byte{"lines": 'n'})
	if err != nil {
		return errPlain(c, err.Error())
	}
	mode := "edges" // head: first N, tail: last N
	n := 10
	if v, ok := vals['n']; ok {
		switch {
		case strings.HasPrefix(v, "+"):
			k, perr := strconv.Atoi(v[1:])
			if perr != nil || k < 1 {
				return errPlain(c, "invalid number of lines: '"+v+"'")
			}
			if tail {
				// tail -n +N prints from line N onward
				mode, n = "from", k
			} else {
				n = k // head -n +N == head -n N
			}
		case strings.HasPrefix(v, "-") && v != "-":
			k, perr := strconv.Atoi(v[1:])
			if perr != nil || k < 0 {
				return errPlain(c, "invalid number of lines: '"+v+"'")
			}
			if tail {
				return errPlain(c, "invalid number of lines: '"+v+"'")
			}
			// head -n -N prints all but the last N lines
			mode, n = "allButLast", k
		default:
			k, perr := strconv.Atoi(v)
			if perr != nil || k < 0 {
				return errPlain(c, "invalid number of lines: '"+v+"'")
			}
			n = k
		}
	}
	if len(operands) == 0 {
		operands = []string{"-"}
	}
	showHeaders := len(operands) > 1
	code := 0
	for _, p := range operands {
		content, rcode := readOneOperand(c, s, p)
		if rcode != 0 {
			code = rcode
			continue
		}
		if showHeaders {
			fmt.Fprintf(c.Stdout, "==> %s <==\n", p)
		}
		lines := splitLines(content)
		var kept []string
		switch mode {
		case "from":
			if n-1 < len(lines) {
				kept = lines[n-1:]
			}
		case "allButLast":
			if n < len(lines) {
				kept = lines[:len(lines)-n]
			}
		default:
			if n < len(lines) {
				if tail {
					kept = lines[len(lines)-n:]
				} else {
					kept = lines[:n]
				}
			} else {
				kept = lines
			}
		}
		for _, l := range kept {
			fmt.Fprintln(c.Stdout, l)
		}
	}
	return code
}

// expandOldCount rewrites the classic `head -5` syntax into `-n 5`.
// A digit-only word right after `-n` is that flag's value, not an old-style
// count (so `head -n -2` survives intact).
func expandOldCount(argv []string) []string {
	out := make([]string, 0, len(argv))
	prevWantsValue := false
	for _, a := range argv {
		if prevWantsValue {
			prevWantsValue = false
			out = append(out, a)
			continue
		}
		if len(a) > 1 && a[0] == '-' {
			allDigits := true
			for _, ch := range a[1:] {
				if ch < '0' || ch > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				out = append(out, "-n", a[1:])
				prevWantsValue = true
				continue
			}
			prevWantsValue = a == "-n"
		}
		out = append(out, a)
	}
	return out
}

// splitLines splits input into lines, dropping a trailing empty line
// that appears when content ends with \n.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	return lines
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}
