package vshell

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// registerAll installs every built-in command.
func (s *Shell) registerAll() {
	for _, group := range [][]Cmd{
		navCmds(), fileCmds(), viewCmds(), searchCmds(), textCmds(),
		permCmds(), archiveCmds(), procCmds(), sysCmds(), netCmds(),
		cronCmds(), scriptCmds(), sedCmds(), awkCmds(),
	} {
		for _, c := range group {
			s.commands[c.Name] = &c
		}
	}
}

// CommandNames returns the sorted list of registered command names.
func (s *Shell) CommandNames() []string {
	names := make([]string, 0, len(s.commands))
	for name := range s.commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// LookupCommand returns the Cmd for name, or nil.
func (s *Shell) LookupCommand(name string) *Cmd {
	return s.commands[name]
}

/*
parseArgs is a tiny getopt: spec letters without ':' are boolean flags,
letters with ':' take a value. Short flags may be combined (-la, -n5, -n 5).
long maps --name to a spec letter (--name=value or --name value).
"--" ends option parsing. Remaining words become operands.
*/
func parseArgs(spec string, argv []string, long map[string]byte) (
	bools map[byte]bool, vals map[byte]string, operands []string, err error) {

	bools, vals = map[byte]bool{}, map[byte]string{}
	valueFlag := func(c byte) bool {
		return strings.IndexByte(spec, c) >= 0 &&
			strings.IndexByte(spec, c)+1 < len(spec) &&
			spec[strings.IndexByte(spec, c)+1] == ':'
	}

	i := 0
	noMore := false
	for i < len(argv) {
		a := argv[i]
		switch {
		case noMore || a == "" || a[0] != '-' || a == "-":
			operands = append(operands, a)
		case a == "--":
			noMore = true
		case strings.HasPrefix(a, "--"):
			name := a[2:]
			value := ""
			hasValue := false
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name, value, hasValue = name[:eq], name[eq+1:], true
			}
			c, ok := long[name]
			if !ok {
				return bools, vals, operands, fmt.Errorf("unrecognized option '%s'", a)
			}
			if valueFlag(c) {
				if !hasValue {
					if i+1 >= len(argv) {
						return bools, vals, operands, fmt.Errorf("option '--%s' requires an argument", name)
					}
					i++
					value = argv[i]
				}
				vals[c] = value
			} else {
				bools[c] = true
			}
		default: // short options cluster
			cluster := a[1:]
			for j := 0; j < len(cluster); j++ {
				c := cluster[j]
				if strings.IndexByte(spec, c) < 0 {
					return bools, vals, operands, fmt.Errorf("invalid option -- '%c'", c)
				}
				if valueFlag(c) {
					var value string
					if j+1 < len(cluster) {
						value = cluster[j+1:]
					} else if i+1 < len(argv) {
						i++
						value = argv[i]
					} else {
						return bools, vals, operands, fmt.Errorf("option requires an argument -- '%c'", c)
					}
					vals[c] = value
					j = len(cluster)
					break
				}
				bools[c] = true
			}
		}
		i++
	}
	return bools, vals, operands, nil
}

// permString renders permission bits as "-rw-r--r--" (with type char).
func permString(n *Node) string {
	var b strings.Builder
	switch n.Type {
	case TypeDir:
		b.WriteByte('d')
	case TypeSymlink:
		b.WriteByte('l')
	default:
		b.WriteByte('-')
	}
	bits := n.Perms
	for shift := 6; shift >= 0; shift -= 3 {
		triplet := bits >> uint(shift) & 7
		if triplet&4 != 0 {
			b.WriteByte('r')
		} else {
			b.WriteByte('-')
		}
		if triplet&2 != 0 {
			b.WriteByte('w')
		} else {
			b.WriteByte('-')
		}
		if triplet&1 != 0 {
			b.WriteByte('x')
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// humanSize renders a byte count like ls -h: 12K, 4.0K, 1.2M, 3G.
func humanSize(n int) string {
	f := float64(n)
	switch {
	case f >= 1<<30:
		if f >= 10<<30 {
			return fmt.Sprintf("%.0fG", f/(1<<30))
		}
		return fmt.Sprintf("%.1fG", f/(1<<30))
	case f >= 1<<20:
		if f >= 10<<20 {
			return fmt.Sprintf("%.0fM", f/(1<<20))
		}
		return fmt.Sprintf("%.1fM", f/(1<<20))
	case f >= 1<<10:
		if f >= 10<<10 {
			return fmt.Sprintf("%.0fK", f/(1<<10))
		}
		return fmt.Sprintf("%.1fK", f/(1<<10))
	default:
		return fmt.Sprintf("%d", n)
	}
}

// errNode formats a command error like real coreutils do.
func errNode(c *Ctx, operand string, err error) int {
	fmt.Fprintf(c.Stderr, "%s: %s: %s\n", c.Args[0], operand, err)
	return 1
}

// breToRE converts a POSIX basic regular expression (BRE) as used by sed
// and grep into Go RE2 syntax. In BRE, \( \) \{ \} are the group/interval
// operators while bare + ? | ( ) { } are literals — the opposite of RE2.
// GNU extensions \+ \? \| are mapped to their ERE meanings.
func breToRE(pat string) string {
	var b strings.Builder
	inClass := false
	for i := 0; i < len(pat); i++ {
		c := pat[i]
		switch {
		case inClass:
			b.WriteByte(c)
			if c == ']' {
				inClass = false
			}
		case c == '[':
			inClass = true
			b.WriteByte(c)
			if i+1 < len(pat) && (pat[i+1] == '^' || pat[i+1] == '!') {
				b.WriteByte(pat[i+1])
				i++
			}
		case c == '\\' && i+1 < len(pat):
			switch pat[i+1] {
			case '(', ')', '{', '}':
				b.WriteByte(pat[i+1]) // BRE operators -> RE2 metachars
			case '+', '?', '|':
				b.WriteByte(pat[i+1]) // GNU extensions -> ERE meaning
			case '<', '>':
				b.WriteString("\\b") // word boundaries
				i++
				continue
			default:
				b.WriteByte(c)
				b.WriteByte(pat[i+1])
			}
			i++
		case c == '+' || c == '?' || c == '|' || c == '{' || c == '}' || c == '(' || c == ')':
			// bare metachars are literals in BRE
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// errPlain prints a plain command error.
func errPlain(c *Ctx, msg string) int {
	fmt.Fprintf(c.Stderr, "%s: %s\n", c.Args[0], msg)
	return 1
}

// printfFormat expands a printf-style format with %s/%d/%f/%% and args.
// Like real printf, the format is reused as often as needed to consume
// all arguments.
func printfFormat(format string, args []string) string {
	var b strings.Builder
	argi := 0
	for {
		prev := argi
		argi = printfPass(&b, format, args, argi)
		if argi >= len(args) || argi == prev {
			break
		}
	}
	return b.String()
}

// printfPass applies the format once starting at argument index argi.
func printfPass(b *strings.Builder, format string, args []string, argi int) int {
	for i := 0; i < len(format); i++ {
		ch := format[i]
		if ch != '%' {
			b.WriteByte(ch)
			continue
		}
		if i+1 >= len(format) {
			b.WriteByte('%')
			break
		}
		// scan optional flags/width/precision
		j := i + 1
		for j < len(format) && strings.IndexByte("-+ #0123456789.", format[j]) >= 0 {
			j++
		}
		if j >= len(format) {
			b.WriteByte('%')
			break
		}
		spec := format[i : j+1]
		verb := format[j]
		var arg string
		if argi < len(args) {
			arg = args[argi]
		}
		argi++
		switch verb {
		case '%':
			fmt.Fprintf(b, "%%")
			argi--
		case 'd', 'i':
			f, _ := strconv.ParseFloat(strings.TrimSpace(arg), 64)
			fmt.Fprintf(b, spec[:len(spec)-1]+"d", int64(f))
		case 'f', 'g', 'e':
			f, _ := strconv.ParseFloat(strings.TrimSpace(arg), 64)
			fmt.Fprintf(b, spec[:len(spec)-1]+string(verb), f)
		case 's':
			fmt.Fprintf(b, spec[:len(spec)-1]+"s", arg)
		case 'x', 'X', 'o':
			f, _ := strconv.ParseFloat(strings.TrimSpace(arg), 64)
			fmt.Fprintf(b, spec[:len(spec)-1]+string(verb), int64(f))
		case 'c':
			for _, r := range arg {
				b.WriteString(string(r))
				break
			}
		default:
			b.WriteString(spec)
			argi--
		}
		i = j
	}
	return argi
}
