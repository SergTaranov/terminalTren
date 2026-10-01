package vshell

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Result is the outcome of executing one command line.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Clear    bool // the clear command was requested
}

// Process is one entry of the simulated process table.
type Process struct {
	PID   int
	User  string
	Cmd   string
	CPU   float64
	Mem   float64
	State string
	Time  string
}

// Unit is a simulated systemd unit.
type Unit struct {
	Name    string // e.g. nginx.service
	Desc    string
	Loaded  string
	Active  string // active, inactive, failed
	Enabled bool
	Logs    []string
}

// Cmd is a registered shell command.
type Cmd struct {
	Name string
	Man  string // short Russian manual shown by `man`
	Run  func(c *Ctx) int
}

// Ctx carries everything a command implementation needs.
type Ctx struct {
	Shell     *Shell
	Args      []string // Args[0] is the command name
	Stdin     string
	Stdout    *bytes.Buffer
	Stderr    *bytes.Buffer
	clearFlag bool
}

// Shell is the virtual shell state.
type Shell struct {
	FS              *FS
	Env             map[string]string
	Cwd             string
	User            string
	Umask           uint32
	History         []string
	Aliases         map[string]string
	Procs           []*Process
	Crontab         []string
	Units           map[string]*Unit
	Web             map[string]string // fake HTTP responses for curl
	commands        map[string]*Cmd
	functions       map[string]interface{} // shell function bodies (set by script.go)
	localFrames     []*localFrame          // `local` variable scopes of active functions
	params          []string               // positional parameters $1..
	prevDir         string
	lastExit        int
	suppressHistory bool
	subDepth        int // $(...) nesting guard
	aliasDepth      int // alias expansion re-split guard
}

// NewShell creates a shell with a fresh filesystem and all builtins registered.
func NewShell() *Shell {
	s := &Shell{
		FS:        NewFS(),
		Env:       map[string]string{},
		Cwd:       "/home/user",
		User:      "user",
		Umask:     0o022,
		Aliases:   map[string]string{},
		Units:     map[string]*Unit{},
		Web:       map[string]string{},
		commands:  map[string]*Cmd{},
		functions: map[string]interface{}{},
	}
	s.Env["HOME"] = s.FS.Home
	s.Env["USER"] = "user"
	s.Env["PWD"] = s.Cwd
	s.Env["SHELL"] = "/bin/bash"
	s.Env["PATH"] = "/usr/local/bin:/usr/bin:/bin"
	s.Env["HOSTNAME"] = "trenbox"
	s.Env["TERM"] = "xterm-256color"
	s.Env["LANG"] = "en_US.UTF-8"
	s.registerAll()
	return s
}

// Prompt returns the shell prompt string, e.g. "user@trenbox:~$ ".
func (s *Shell) Prompt() string {
	cwd := s.Cwd
	if cwd == s.FS.Home {
		cwd = "~"
	} else if strings.HasPrefix(cwd, s.FS.Home+"/") {
		cwd = "~" + strings.TrimPrefix(cwd, s.FS.Home)
	}
	marker := "$"
	if s.User == "root" {
		marker = "#"
	}
	return fmt.Sprintf("%s@%s:%s%s ", s.User, s.Env["HOSTNAME"], cwd, marker)
}

func (s *Shell) getenv(name string) string {
	switch name {
	case "?":
		return strconv.Itoa(s.lastExit)
	case "PWD":
		return s.Cwd
	case "#":
		return strconv.Itoa(len(s.params))
	case "@":
		return strings.Join(s.params, " ")
	case "*":
		return strings.Join(s.params, " ")
	}
	if name == "0" {
		return "bash"
	}
	if n, err := strconv.Atoi(name); err == nil && n >= 1 {
		if n-1 < len(s.params) {
			return s.params[n-1]
		}
		return ""
	}
	return s.Env[name]
}

// Run executes one command line and returns its combined output.
func (s *Shell) Run(line string) *Result {
	res := &Result{}
	line = strings.TrimSpace(line)
	if line == "" {
		return res
	}
	if !s.suppressHistory {
		s.History = append(s.History, line)
	}

	var stdout, stderr bytes.Buffer
	res.ExitCode = s.runProtected(line, &stdout, &stderr, res)
	res.Stdout = stdout.String()
	res.Stderr = stderr.String()
	s.lastExit = res.ExitCode
	return res
}

// runProtected executes a command line, converting any panic of the
// simulator into a command error instead of crashing the whole trainer.
func (s *Shell) runProtected(line string, stdout, stderr io.Writer, res *Result) (code int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "bash: internal trainer error: %v\n", r)
			code = 1
		}
	}()
	return s.execCommandLine(line, stdout, stderr, res)
}

func (s *Shell) expandAliases(line string) (string, bool) {
	expandedAny := false
	for i := 0; i < 5; i++ {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			return line, expandedAny
		}
		expanded, ok := s.Aliases[fields[0]]
		if !ok {
			return line, expandedAny
		}
		line = expanded + strings.TrimPrefix(line, fields[0])
		expandedAny = true
	}
	return line, expandedAny
}

// runSubshell implements $(command) substitution.
func (s *Shell) runSubshell(cmd string) string {
	if s.subDepth > 8 {
		return ""
	}
	s.subDepth++
	suppress := s.suppressHistory
	s.suppressHistory = true
	res := s.Run(cmd)
	s.suppressHistory = suppress
	s.subDepth--
	// bash strips all trailing newlines from $(...), not just one
	return strings.TrimRight(res.Stdout, "\n")
}

// segment is a chunk of the command line delimited by ;, && or ||.
type segment struct {
	text string
	conn byte // connector to the NEXT segment: 0 none, ';' '&&' '||' as ';','&','|'
}

// splitOperators splits a raw command line by ;, && and ||, respecting
// quotes and $() nesting. Each segment is tokenized and expanded only when
// it runs, so variables and aliases defined earlier in the line are visible
// to later segments (like in real bash).
func splitOperators(line string) []segment {
	var segs []segment
	var cur strings.Builder
	flush := func(conn byte) {
		segs = append(segs, segment{text: cur.String(), conn: conn})
		cur.Reset()
	}
	depth := 0 // $( nesting level
	i := 0
	for i < len(line) {
		c := line[i]
		switch {
		case c == '\\' && i+1 < len(line):
			cur.WriteByte(c)
			cur.WriteByte(line[i+1])
			i += 2
		case c == '\'':
			j := i + 1
			for j < len(line) && line[j] != '\'' {
				j++
			}
			if j < len(line) {
				j++
			}
			cur.WriteString(line[i:j])
			i = j
		case c == '"':
			j := i + 1
			for j < len(line) {
				if line[j] == '\\' && j+1 < len(line) {
					j += 2
					continue
				}
				if line[j] == '"' {
					j++
					break
				}
				j++
			}
			cur.WriteString(line[i:j])
			i = j
		case c == '$' && i+1 < len(line) && line[i+1] == '(':
			depth++
			cur.WriteString("$(")
			i += 2
		case c == ')' && depth > 0:
			depth--
			cur.WriteByte(c)
			i++
		case depth == 0 && c == ';':
			flush(';')
			i++
		case depth == 0 && c == '&' && i+1 < len(line) && line[i+1] == '&':
			flush('&')
			i += 2
		case depth == 0 && c == '&':
			// background separator: what follows always runs
			flush(0)
			i++
		case depth == 0 && c == '|' && i+1 < len(line) && line[i+1] == '|':
			flush('|')
			i += 2
		default:
			cur.WriteByte(c)
			i++
		}
	}
	segs = append(segs, segment{text: cur.String()})
	return segs
}

// SplitSegments exposes splitOperators for the exercise checker: it splits a
// compound command into its parts without executing anything.
func SplitSegments(line string) []string {
	segs := splitOperators(line)
	parts := make([]string, len(segs))
	for i, seg := range segs {
		parts[i] = seg.text
	}
	return parts
}

// execCommandLine runs the raw line: splits into segments, then executes
// each with the ; / && / || rules.
func (s *Shell) execCommandLine(line string, stdout, stderr io.Writer, res *Result) int {
	segs := splitOperators(line)
	code := 0
	for i, seg := range segs {
		if i > 0 {
			switch segs[i-1].conn {
			case '&':
				if code != 0 {
					continue
				}
			case '|':
				if code == 0 {
					continue
				}
			}
		}
		text := strings.TrimSpace(seg.text)
		if text == "" {
			continue
		}
		if expanded, ok := s.expandAliases(text); ok && s.aliasDepth < 3 {
			// the alias body may itself contain ; && || — re-split it
			s.aliasDepth++
			code = s.execCommandLine(expanded, stdout, stderr, res)
			s.aliasDepth--
			s.lastExit = code
			continue
		}
		toks := tokenize(text, s.getenv, s.runSubshell)
		code = s.execPipeline(toks, stdout, stderr, res)
		s.lastExit = code
	}
	return code
}

// stage is one simple command in a pipeline.
type stage struct {
	argv    []string
	quoted  []bool
	redirs  []Token  // redirection operators in order
	redPath []string // their target paths
}

// execPipeline runs one pipeline (stages joined by |).
func (s *Shell) execPipeline(toks []Token, stdout, stderr io.Writer, res *Result) int {
	var stages []*stage
	cur := &stage{}
	expectPath := -1 // index of a redirection awaiting its target word

	for _, t := range toks {
		if expectPath >= 0 {
			if t.Kind != TokWord {
				fmt.Fprintln(stderr, "bash: syntax error near unexpected token")
				return 2
			}
			cur.redPath[expectPath] = t.Text
			expectPath = -1
			continue
		}
		switch t.Kind {
		case TokWord:
			cur.argv = append(cur.argv, t.Text)
			cur.quoted = append(cur.quoted, t.Quoted)
		case TokPipe:
			stages = append(stages, cur)
			cur = &stage{}
		case TokRedirOut, TokRedirApp, TokRedirIn, TokRedirErr:
			cur.redirs = append(cur.redirs, t)
			cur.redPath = append(cur.redPath, "")
			expectPath = len(cur.redirs) - 1
		default:
			fmt.Fprintln(stderr, "bash: syntax error near unexpected token")
			return 2
		}
	}
	if expectPath >= 0 {
		fmt.Fprintln(stderr, "bash: syntax error near unexpected token `newline'")
		return 2
	}
	stages = append(stages, cur)
	if len(stages[0].argv) == 0 && len(stages[0].redirs) == 0 {
		return 0
	}

	stdin := ""
	code := 0
	for i, st := range stages {
		// like in bash, every stage runs even if an earlier one failed;
		// the pipeline status is the status of its last stage
		var out, errb string
		code, out, errb = s.execStage(st, stdin, res)
		if i == len(stages)-1 && out != "" {
			fmt.Fprint(stdout, out)
		}
		if errb != "" {
			fmt.Fprint(stderr, errb)
		}
		stdin = out
	}
	return code
}

// execStage runs a single simple command with its redirections and returns
// its exit code, captured stdout and stderr. Output sent to files by
// redirections does not appear in the returned stdout, like in real bash.
func (s *Shell) execStage(st *stage, stdin string, res *Result) (int, string, string) {
	// glob expansion on unquoted args
	var argv []string
	for i, a := range st.argv {
		if i < len(st.quoted) && st.quoted[i] || !hasWildcard(a) {
			argv = append(argv, a)
			continue
		}
		matches := s.FS.Glob(s.Cwd, a)
		if len(matches) == 0 {
			argv = append(argv, a)
			continue
		}
		// keep the literal prefix of the pattern in the results, like
		// bash: `echo ~/*.txt` prints ~/a.txt, `echo ../*` prints ../name
		dirPart := ""
		if i := strings.LastIndexByte(a, '/'); i >= 0 {
			dirPart = a[:i+1]
		}
		resolved := s.FS.AbsPath(s.Cwd, dirPart)
		for _, m := range matches {
			if rel := strings.TrimPrefix(m, resolved+"/"); rel != m {
				argv = append(argv, dirPart+rel)
			} else {
				argv = append(argv, m)
			}
		}
	}

	// input redirection
	for i, r := range st.redirs {
		if r.Kind == TokRedirIn {
			content, err := s.FS.ReadFile(s.Cwd, st.redPath[i])
			if err != nil {
				return 1, "", fmt.Sprintf("bash: %s: %s\n", st.redPath[i], err)
			}
			stdin = content
		}
	}

	// env assignment prefix: VAR=value ... cmd
	envSet := map[string]string{}
	for len(argv) > 0 && isAssign(argv[0]) {
		kv := strings.SplitN(argv[0], "=", 2)
		envSet[kv[0]] = kv[1]
		argv = argv[1:]
	}
	noCmd := len(argv) == 0

	var outBuf, errBuf bytes.Buffer
	code := 0
	if !noCmd {
		sudo := false
		if argv[0] == "sudo" {
			if len(argv) == 1 {
				return 0, "", "usage: sudo command\n"
			}
			sudo = true
			argv = argv[1:]
		}

		c := &Ctx{Shell: s, Args: argv, Stdin: stdin, Stdout: &outBuf, Stderr: &errBuf}

		oldUser, oldParams := s.User, s.params
		if sudo {
			s.User = "root"
		}
		savedEnv, savedHad := map[string]string{}, map[string]bool{}
		for k, v := range envSet {
			savedEnv[k], savedHad[k] = s.Env[k], true
			if _, ok := s.Env[k]; !ok {
				savedHad[k] = false
			}
			s.Env[k] = v
		}

		code = s.dispatch(c)
		if c.clearFlag {
			res.Clear = true
		}

		if sudo {
			s.User = oldUser
		}
		s.params = oldParams
		for k := range envSet {
			if savedHad[k] {
				s.Env[k] = savedEnv[k]
			} else {
				delete(s.Env, k)
			}
		}
	} else {
		// bare assignments persist; `> file` with no command creates it
		for k, v := range envSet {
			s.Env[k] = v
		}
	}

	// apply output redirections to the virtual FS
	owner := s.User
	for i, r := range st.redirs {
		target := st.redPath[i]
		mode := 0o644 & ^s.Umask
		appendMode := r.Kind == TokRedirApp || r.Kind == TokRedirErr && r.Text == ">>"
		if !s.redirectAllowed(target) {
			fmt.Fprintf(&errBuf, "bash: %s: Permission denied\n", target)
			code = 1
			continue
		}
		switch r.Kind {
		case TokRedirOut, TokRedirApp:
			content := outBuf.String()
			outBuf.Reset()
			var err error
			if appendMode {
				err = s.FS.AppendFile(s.Cwd, target, content, mode, owner, owner)
			} else {
				err = s.FS.WriteFile(s.Cwd, target, content, mode, owner, owner)
			}
			if err != nil {
				fmt.Fprintf(&errBuf, "bash: %s: %s\n", target, err)
				code = 1
			}
		case TokRedirErr:
			content := errBuf.String()
			errBuf.Reset()
			var err error
			if appendMode {
				err = s.FS.AppendFile(s.Cwd, target, content, mode, owner, owner)
			} else {
				err = s.FS.WriteFile(s.Cwd, target, content, mode, owner, owner)
			}
			if err != nil {
				fmt.Fprintf(&errBuf, "bash: %s: %s\n", target, err)
				code = 1
			}
		}
	}

	return code, outBuf.String(), errBuf.String()
}

func isAssign(w string) bool {
	eq := strings.IndexByte(w, '=')
	return eq > 0
}

// redirectAllowed reports whether user may open target for writing
// (creating or truncating). Missing parents let the FS layer report its
// own "No such file or directory" error, so only permissions are checked.
func (s *Shell) redirectAllowed(target string) bool {
	if n, err := s.FS.LookupNoFollow(s.Cwd, target); err == nil {
		if n.Type == TypeSymlink {
			if tn, terr := s.FS.Lookup(s.Cwd, target); terr == nil && tn.Type != TypeDir {
				return CanWrite(tn, s.User)
			}
			return true // dangling link or directory: let Create report it
		}
		if n.Type == TypeDir {
			return true // Create reports "Is a directory"
		}
		return CanWrite(n, s.User)
	}
	parent, _, err := s.FS.splitParent(s.Cwd, target)
	if err != nil {
		return true
	}
	return CanWrite(parent, s.User) && CanExec(parent, s.User)
}

// dispatch finds and runs the command for a prepared Ctx.
func (s *Shell) dispatch(c *Ctx) int {
	name := c.Args[0]

	// scripts and external paths: ./x.sh, /path/to/x.sh
	if strings.Contains(name, "/") {
		return runScriptFile(c, true)
	}

	if cmd, ok := s.commands[name]; ok {
		return cmd.Run(c)
	}

	if code, ok := s.callFunction(name, c); ok {
		return code
	}

	fmt.Fprintf(c.Stderr, "%s: command not found\n", name)
	return 127
}

// ---- permission helpers ----

func userGroup(user string) string { return user }

func classBits(n *Node, user string) uint32 {
	switch {
	case user == "root":
		// root reads and writes anything, but can only execute files
		// that have at least one execute bit set
		if n.Perms&0o111 != 0 {
			return 0o7
		}
		return 0o6
	case n.Owner == user:
		return n.Perms >> 6 & 7
	case n.Group == userGroup(user):
		return n.Perms >> 3 & 7
	default:
		return n.Perms & 7
	}
}

// CanRead reports whether user may read node n.
func CanRead(n *Node, user string) bool { return classBits(n, user)&4 != 0 }

// CanWrite reports whether user may write node n.
func CanWrite(n *Node, user string) bool { return classBits(n, user)&2 != 0 }

// CanExec reports whether user may execute node n.
func CanExec(n *Node, user string) bool { return classBits(n, user)&1 != 0 }

// mayEnter reports whether user may list/traverse directory n (r+x).
func mayEnter(n *Node, user string) bool { return classBits(n, user)&5 == 5 }
