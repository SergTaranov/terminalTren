package vshell

import (
	"strings"
)

// TokKind enumerates token kinds produced by the lexer.
type TokKind int

const (
	TokWord TokKind = iota
	TokPipe
	TokSemi
	TokAndAnd
	TokOrOr
	TokRedirOut // >
	TokRedirApp // >>
	TokRedirIn  // <
	TokRedirErr // 2> or 2>>
)

// Token is one lexical item of a command line.
type Token struct {
	Kind   TokKind
	Text   string
	Quoted bool // word came from quotes: suppress globbing
}

// subRunner executes a $(...) substitution and returns its stdout.
type subRunner func(cmd string) string

// tokenize splits a command line into words and operators, handling single
// quotes (no expansion), double quotes (expansion), backslash escapes,
// $VAR / ${VAR}, $(command) and $((expression)) expansion.
func tokenize(line string, getenv func(string) string, runSub subRunner) []Token {
	var toks []Token
	var cur strings.Builder
	quoted := false // current word is quoted
	hasWord := false

	flush := func() {
		if hasWord {
			toks = append(toks, Token{Kind: TokWord, Text: cur.String(), Quoted: quoted})
			cur.Reset()
			quoted = false
			hasWord = false
		}
	}
	operator := func(k TokKind) {
		flush()
		toks = append(toks, Token{Kind: k})
	}

	i := 0
	for i < len(line) {
		c := line[i]
		switch {
		case c == ' ' || c == '\t':
			flush()
			i++
		case c == '\\' && i+1 < len(line):
			quoted = true
			cur.WriteByte(line[i+1])
			hasWord = true
			i += 2
		case c == '\'':
			quoted = true
			hasWord = true
			j := i + 1
			for j < len(line) && line[j] != '\'' {
				j++
			}
			cur.WriteString(line[i+1 : j])
			i = j + 1
		case c == '"':
			quoted = true
			hasWord = true
			i++
			for i < len(line) && line[i] != '"' {
				switch {
				case line[i] == '\\' && i+1 < len(line) && (line[i+1] == '"' || line[i+1] == '\\' || line[i+1] == '$'):
					cur.WriteByte(line[i+1])
					i += 2
				case line[i] == '$':
					val, n, _ := expandDollar(line[i:], getenv, runSub)
					cur.WriteString(val)
					i += n
				default:
					cur.WriteByte(line[i])
					i++
				}
			}
			i++ // closing quote
		case c == '$':
			val, n, multi := expandDollar(line[i:], getenv, runSub)
			if multi {
				// unquoted substitution: split output into separate words;
				// a word prefix built so far joins the first field
				fields := strings.Fields(val)
				if hasWord || cur.Len() > 0 {
					if len(fields) > 0 {
						cur.WriteString(fields[0])
						fields = fields[1:]
					}
					flush()
				}
				for _, f := range fields {
					toks = append(toks, Token{Kind: TokWord, Text: f})
				}
			} else {
				cur.WriteString(val)
				hasWord = true
			}
			i += n
		case c == '|':
			if i+1 < len(line) && line[i+1] == '|' {
				operator(TokOrOr)
				i += 2
			} else {
				operator(TokPipe)
				i++
			}
		case c == '&':
			if i+1 < len(line) && line[i+1] == '&' {
				operator(TokAndAnd)
				i += 2
			} else {
				operator(TokSemi)
				i++
			}
		case c == ';':
			operator(TokSemi)
			i++
		case c == '<':
			operator(TokRedirIn)
			i++
		case c == '>':
			if cur.String() == "2" && !quoted && hasWord {
				cur.Reset()
				hasWord = false
				if i+1 < len(line) && line[i+1] == '>' {
					toks = append(toks, Token{Kind: TokRedirErr, Text: ">>"})
					i += 2
				} else {
					toks = append(toks, Token{Kind: TokRedirErr, Text: ">"})
					i++
				}
				continue
			}
			if i+1 < len(line) && line[i+1] == '>' {
				operator(TokRedirApp)
				i += 2
			} else {
				operator(TokRedirOut)
				i++
			}
		default:
			cur.WriteByte(c)
			hasWord = true
			i++
		}
	}
	flush()
	return toks
}

// expandDollar expands a $-construct at the start of s. It returns the value,
// the number of bytes consumed, and multi=true when the expansion happened
// outside quotes and may produce several words (command substitution).
func expandDollar(s string, getenv func(string) string, runSub subRunner) (string, int, bool) {
	if len(s) < 2 {
		return "$", 1, false
	}
	if s[1] == '(' {
		end := findMatching(s, 1, "(", ")")
		if end < 0 {
			return s, len(s), false
		}
		if strings.HasPrefix(s, "$((") && end >= 4 {
			expr := s[3 : end-2]
			return evalArith(expr, getenv), end, false
		}
		cmd := s[2 : end-1]
		if runSub == nil {
			return "", end, true
		}
		return runSub(cmd), end, true
	}
	if s[1] == '{' {
		if end := strings.IndexByte(s, '}'); end > 0 {
			return getenv(s[2:end]), end + 1, false
		}
		return s, len(s), false
	}
	switch s[1] {
	case '?', '#', '$', '@', '*', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return getenv(s[1:2]), 2, false
	}
	j := 1
	for j < len(s) && (isAlnum(s[j]) || s[j] == '_') {
		j++
	}
	if j == 1 {
		return "$", 1, false
	}
	return getenv(s[1:j]), j, false
}

// findMatching returns the index just past the closing closer that matches
// the opener at position openPos, honoring nesting and quotes.
func findMatching(s string, openPos int, opener, closer string) int {
	depth := 1
	i := openPos + len(opener)
	for i < len(s) {
		switch s[i] {
		case '\'', '"':
			q := s[i]
			i++
			for i < len(s) && s[i] != q {
				i++
			}
			i++
			continue
		case '(':
			if opener == "(" {
				depth++
			}
		case ')':
			if opener == "(" {
				depth--
				if depth == 0 {
					return i + 1
				}
			}
		}
		i++
	}
	return -1
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
