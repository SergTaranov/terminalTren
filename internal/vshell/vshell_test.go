package vshell

import (
	"strings"
	"testing"
)

// run runs commands on a fresh shell and returns the last result.
func run(t *testing.T, cmds ...string) *Shell {
	t.Helper()
	s := NewShell()
	for _, cmd := range cmds {
		s.Run(cmd)
	}
	return s
}

func out(t *testing.T, s *Shell, cmd string) string {
	t.Helper()
	res := s.Run(cmd)
	if res.Stderr != "" {
		t.Logf("stderr of %q: %s", cmd, res.Stderr)
	}
	return res.Stdout
}

func expect(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("output mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestNavigation(t *testing.T) {
	s := run(t, "mkdir projects", "cd projects", "pwd")
	expect(t, s.Cwd, "/home/user/projects")
	expect(t, out(t, s, "pwd"), "/home/user/projects\n")
	s.Run("cd ..")
	expect(t, out(t, s, "pwd"), "/home/user\n")
	s.Run("cd")
	expect(t, s.Cwd, "/home/user")
	s.Run("cd /tmp")
	s.Run("cd -")
	expect(t, s.Cwd, "/home/user")
}

func TestLs(t *testing.T) {
	s := run(t, "touch b.txt a.txt .hidden", "mkdir sub")
	expect(t, out(t, s, "ls"), "a.txt b.txt sub\n")
	if !strings.HasPrefix(out(t, s, "ls -a"), ".") {
		t.Error("ls -a should show dotfiles")
	}
	long := out(t, s, "ls -l")
	if !strings.Contains(long, "-rw-r--r--") || !strings.Contains(long, "a.txt") || !strings.Contains(long, "total") {
		t.Errorf("ls -l output wrong:\n%s", long)
	}
	if got := out(t, s, "ls sub"); got != "" {
		t.Errorf("ls of empty dir should print nothing, got %q", got)
	}
}

func TestTree(t *testing.T) {
	s := run(t, "mkdir -p docs/img", "touch docs/a.md docs/img/b.png")
	tree := out(t, s, "tree")
	if !strings.Contains(tree, "docs") || !strings.Contains(tree, "img") || !strings.Contains(tree, "b.png") ||
		!strings.Contains(tree, "2 directories, 2 files") {
		t.Errorf("tree output wrong:\n%s", tree)
	}
}

func TestFileOps(t *testing.T) {
	s := run(t)
	s.Run("echo hello > f.txt")
	expect(t, out(t, s, "cat f.txt"), "hello\n")
	s.Run("cp f.txt g.txt")
	expect(t, out(t, s, "cat g.txt"), "hello\n")
	s.Run("mv g.txt h.txt")
	if _, err := s.FS.Lookup(s.Cwd, "g.txt"); err == nil {
		t.Error("g.txt should be gone after mv")
	}
	s.Run("rm f.txt h.txt")
	if _, err := s.FS.Lookup(s.Cwd, "f.txt"); err == nil {
		t.Error("f.txt should be removed")
	}
	// rm -r required for dirs
	res := s.Run("mkdir d")
	res = s.Run("touch d/x")
	res = s.Run("rmdir d")
	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "not empty") {
		t.Errorf("rmdir non-empty should fail: %v %q", res.ExitCode, res.Stderr)
	}
	res = s.Run("rm d")
	if res.ExitCode == 0 {
		t.Error("rm on dir without -r should fail")
	}
	res = s.Run("rm -r d")
	if res.ExitCode != 0 {
		t.Errorf("rm -r failed: %s", res.Stderr)
	}
}

func TestCatHeadTailWc(t *testing.T) {
	s := run(t, "printf 'l1\nl2\nl3\nl4\nl5\n' > f")
	expect(t, out(t, s, "head -n 2 f"), "l1\nl2\n")
	expect(t, out(t, s, "tail -n 1 f"), "l5\n")
	expect(t, out(t, s, "head f"), "l1\nl2\nl3\nl4\nl5\n")
	expect(t, out(t, s, "wc -l f"), "5 f\n")
	expect(t, out(t, s, "wc -w f"), "5 f\n")
	expect(t, out(t, s, "wc -l < f"), "5\n")
	nl := out(t, s, "cat -n f")
	if !strings.Contains(nl, "1  l1") && !strings.Contains(nl, "1	l1") {
		t.Errorf("cat -n wrong: %q", nl)
	}
}

func TestPipesAndRedirects(t *testing.T) {
	s := run(t, "printf 'b\na\nb\n' > f")
	expect(t, out(t, s, "sort f"), "a\nb\nb\n")
	expect(t, out(t, s, "sort f | uniq"), "a\nb\n")
	expect(t, out(t, s, "sort f | uniq -c"), "      1 a\n      2 b\n")
	expect(t, out(t, s, "cat f | wc -l"), "3\n")
	expect(t, out(t, s, "sort f | uniq -c | sort -rn"), "      2 b\n      1 a\n")
	s.Run("echo one > log")
	s.Run("echo two >> log")
	expect(t, out(t, s, "cat log"), "one\ntwo\n")
	// input redirect
	expect(t, out(t, s, "wc -l < log"), "2\n")
	// stderr redirect hides error message
	res := s.Run("cat nope 2> err.txt")
	if res.Stderr != "" {
		t.Errorf("2> should capture stderr, got %q", res.Stderr)
	}
	if !strings.Contains(s.FS.mustRead(t, "err.txt"), "No such file") {
		t.Error("2> should write to file")
	}
	// output to file does not pass through the pipe
	s.Run("printf 'x\ny\n' > src")
	expect(t, out(t, s, "cat src | head -n 1 > first"), "")
	expect(t, s.FS.mustRead(t, "first"), "x\n")
}

func TestGrep(t *testing.T) {
	s := run(t, "printf 'apple pie\nBanana split\napple juice\ncherry\n' > f")
	expect(t, out(t, s, "grep apple f"), "apple pie\napple juice\n")
	expect(t, out(t, s, "grep -i banana f"), "Banana split\n")
	expect(t, out(t, s, "grep -v apple f"), "Banana split\ncherry\n")
	g := out(t, s, "grep -n apple f")
	if !strings.Contains(g, "1:apple pie") || !strings.Contains(g, "3:apple juice") {
		t.Errorf("grep -n wrong: %q", g)
	}
	if res := s.Run("grep apple f"); res.ExitCode != 0 {
		t.Error("grep with matches should exit 0")
	}
	if res := s.Run("grep zzz f"); res.ExitCode != 1 {
		t.Error("grep without matches should exit 1")
	}
	s.Run("mkdir -p sub")
	s.Run("printf 'error one\n' > sub/a.log")
	s.Run("printf 'error two\nfine\n' > sub/b.log")
	r := out(t, s, "grep -r error sub")
	if !strings.Contains(r, "a.log") || !strings.Contains(r, "error two") {
		t.Errorf("grep -r wrong: %q", r)
	}
	expect(t, out(t, s, "grep -c apple f"), "2\n")
}

func TestFind(t *testing.T) {
	s := run(t, "mkdir -p docs img", "touch docs/a.txt docs/b.md img/c.txt img/pic.png")
	expect(t, out(t, s, `find . -name "*.txt"`), "./docs/a.txt\n./img/c.txt\n")
	expect(t, out(t, s, "find . -type d"), ".\n./docs\n./img\n")
	expect(t, out(t, s, `find . -name "*.png"`), "./img/pic.png\n")
	res := out(t, s, `find . -name "*.txt" -exec ls {} \;`)
	if !strings.Contains(res, "docs/a.txt") || !strings.Contains(res, "img/c.txt") {
		t.Errorf("find -exec wrong: %q", res)
	}
}

func TestChmod(t *testing.T) {
	s := run(t, "touch secret.txt script.sh")
	s.Run("chmod 600 secret.txt")
	n, _ := s.FS.Lookup(s.Cwd, "secret.txt")
	if n.Perms != 0o600 {
		t.Errorf("chmod 600 failed: %o", n.Perms)
	}
	s.Run("chmod u+x script.sh")
	n, _ = s.FS.Lookup(s.Cwd, "script.sh")
	if n.Perms != 0o744 {
		t.Errorf("chmod u+x failed: %o", n.Perms)
	}
	s.Run("chmod go-rw secret.txt")
	n, _ = s.FS.Lookup(s.Cwd, "secret.txt")
	if n.Perms != 0o600 {
		t.Errorf("chmod go-rw failed: %o", n.Perms)
	}
	res := s.Run("cat secret.txt") // owner can still read
	if res.ExitCode != 0 {
		t.Errorf("owner read failed: %s", res.Stderr)
	}
	s.Run("chmod 000 secret.txt")
	res = s.Run("cat secret.txt")
	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "Permission denied") {
		t.Errorf("chmod 000 should deny read: %q", res.Stderr)
	}
}

func TestChownSudo(t *testing.T) {
	s := run(t, "touch data.txt")
	res := s.Run("chown root data.txt")
	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "Operation not permitted") {
		t.Error("chown as user must fail")
	}
	res = s.Run("sudo chown root data.txt")
	if res.ExitCode != 0 {
		t.Errorf("sudo chown failed: %s", res.Stderr)
	}
	n, _ := s.FS.Lookup(s.Cwd, "data.txt")
	if n.Owner != "root" {
		t.Errorf("owner should be root, got %s", n.Owner)
	}
}

func TestSed(t *testing.T) {
	s := run(t, "printf 'cat dog\ncat hat\n' > f")
	expect(t, out(t, s, "sed s/cat/fox/ f"), "fox dog\nfox hat\n")
	expect(t, out(t, s, "sed 's/cat/fox/g' f"), "fox dog\nfox hat\n")
	s.Run("printf 'one cat two cat\n' > f2")
	expect(t, out(t, s, "sed 's/cat/fox/g' f2"), "one fox two fox\n")
	expect(t, out(t, s, "sed 's/cat/fox/' f2"), "one fox two cat\n")
	s.Run("printf 'keep\nDROP\nkeep2\n' > f3")
	expect(t, out(t, s, "sed '/DROP/d' f3"), "keep\nkeep2\n")
	expect(t, out(t, s, "sed '2d' f3"), "keep\nkeep2\n")
	s.Run("printf 'a\nb\nc\n' > f4")
	expect(t, out(t, s, "sed -n '2p' f4"), "b\n")
	// in place
	s.Run("sed -i 's/a/X/' f4")
	expect(t, s.FS.mustRead(t, "f4"), "X\nb\nc\n")
	// case-insensitive
	s.Run("printf 'Hello World\n' > f5")
	expect(t, out(t, s, "sed 's/world/earth/i' f5"), "Hello earth\n")
}

func TestAwk(t *testing.T) {
	s := run(t, "printf 'alice 30 dev\nbob 25 ops\ncarol 35 dev\n' > staff")
	expect(t, out(t, s, "awk '{print $1}' staff"), "alice\nbob\ncarol\n")
	expect(t, out(t, s, "awk '{print $2}' staff"), "30\n25\n35\n")
	expect(t, out(t, s, "awk '$3 == \"dev\"' staff"), "alice 30 dev\ncarol 35 dev\n")
	expect(t, out(t, s, "awk '$2 > 26' staff"), "alice 30 dev\ncarol 35 dev\n")
	expect(t, out(t, s, "awk 'END{print NR}' staff"), "3\n")
	expect(t, out(t, s, "awk '{s+=$2} END{print s}' staff"), "90\n")
	expect(t, out(t, s, "awk '{print $1, $3}' staff"), "alice dev\nbob ops\ncarol dev\n")
	expect(t, out(t, s, "awk '/dev/' staff"), "alice 30 dev\ncarol 35 dev\n")
	expect(t, out(t, s, "awk 'NR==2' staff"), "bob 25 ops\n")
	expect(t, out(t, s, "awk 'BEGIN{print \"start\"} {print NF}' staff"), "start\n3\n3\n3\n")
	// -F
	s.Run("printf 'root:x:0:0\nuser:x:1000:1000\n' > passwd")
	expect(t, out(t, s, "awk -F: '{print $1}' passwd"), "root\nuser\n")
	// printf and NF
	expect(t, out(t, s, "awk '{printf \"%s-%s\\n\", $1, NF}' staff"), "alice-3\nbob-3\ncarol-3\n")
	expect(t, out(t, s, "awk '{print $NF}' staff"), "dev\nops\ndev\n")
}

func TestEnvExpansion(t *testing.T) {
	s := run(t)
	s.Run("export NAME=World")
	expect(t, out(t, s, "echo $NAME"), "World\n")
	expect(t, out(t, s, `echo "Hi, $NAME!"`), "Hi, World!\n")
	expect(t, out(t, s, "echo ${NAME}123"), "World123\n")
	expect(t, out(t, s, "echo $(echo nested)"), "nested\n")
	expect(t, out(t, s, "echo $((2+3*4))"), "14\n")
	s.Run("true")
	expect(t, out(t, s, "echo $?"), "0\n")
}

func TestIfAndOr(t *testing.T) {
	s := run(t, "touch exists")
	res := s.Run("test -f exists && echo yes")
	if !strings.Contains(res.Stdout, "yes") {
		t.Errorf("&& failed: %q", res.Stdout)
	}
	res = s.Run("test -f nope || echo no")
	if !strings.Contains(res.Stdout, "no") {
		t.Errorf("|| failed: %q", res.Stdout)
	}
	expect(t, out(t, s, "[ -f exists ] && echo bracket"), "bracket\n")
}

func TestScript(t *testing.T) {
	s := run(t)
	s.FS.WriteFile(s.Cwd, "greet.sh", "#!/bin/bash\nNAME=$1\necho \"Hello, $NAME!\"\n", 0o755, "user", "user")
	res := s.Run("./greet.sh Bob")
	if res.Stdout != "Hello, Bob!\n" || res.ExitCode != 0 {
		t.Errorf("script failed: out=%q err=%q code=%d", res.Stdout, res.Stderr, res.ExitCode)
	}

	s.FS.WriteFile(s.Cwd, "cond.sh", "#!/bin/bash\nif [ \"$1\" = \"hi\" ]; then\necho greetings\nelif [ -f data ]; then\necho hasdata\nelse\necho nothing\nfi\n", 0o755, "user", "user")
	expect(t, out(t, s, "./cond.sh hi"), "greetings\n")
	s.Run("touch data")
	expect(t, out(t, s, "./cond.sh x"), "hasdata\n")
	s.Run("rm data")
	expect(t, out(t, s, "./cond.sh x"), "nothing\n")

	s.FS.WriteFile(s.Cwd, "loop.sh", "#!/bin/bash\nfor i in 1 2 3\ndo\necho \"i=$i\"\ndone\n", 0o755, "user", "user")
	expect(t, out(t, s, "./loop.sh"), "i=1\ni=2\ni=3\n")

	s.FS.WriteFile(s.Cwd, "count.sh", "#!/bin/bash\nn=0\nwhile [ $n -lt 3 ]\ndo\nn=$((n+1))\necho n=$n\ndone\n", 0o755, "user", "user")
	expect(t, out(t, s, "./count.sh"), "n=1\nn=2\nn=3\n")

	s.FS.WriteFile(s.Cwd, "func.sh", "#!/bin/bash\ngreet() {\necho \"hi $1\"\n}\ngreet Ann\ngreet Tom\n", 0o755, "user", "user")
	expect(t, out(t, s, "./func.sh"), "hi Ann\nhi Tom\n")

	// executable bit required
	s.FS.WriteFile(s.Cwd, "noexec.sh", "echo x\n", 0o644, "user", "user")
	res = s.Run("./noexec.sh")
	if res.ExitCode != 126 || !strings.Contains(res.Stderr, "Permission denied") {
		t.Errorf("noexec script should fail with 126: %d %q", res.ExitCode, res.Stderr)
	}
	// variables do not leak from scripts
	s.FS.WriteFile(s.Cwd, "leak.sh", "SECRET=42\n", 0o755, "user", "user")
	s.Run("./leak.sh")
	if v, ok := s.Env["SECRET"]; ok {
		t.Errorf("script vars must not leak, got %q", v)
	}
}

func TestTarGzip(t *testing.T) {
	s := run(t, "mkdir proj", "echo data > proj/f.txt", "mkdir proj/sub", "echo deep > proj/sub/g.txt")
	res := s.Run("tar -czf proj.tar.gz proj")
	if res.ExitCode != 0 {
		t.Fatalf("tar create failed: %s", res.Stderr)
	}
	if !strings.Contains(out(t, s, "tar -tzf proj.tar.gz"), "proj/f.txt") {
		t.Error("tar -t should list contents")
	}
	s.Run("mkdir restore")
	s.Run("cd restore")
	s.Run("tar -xzf ../proj.tar.gz")
	if _, err := s.FS.Lookup(s.Cwd, "proj/f.txt"); err != nil {
		t.Fatalf("tar extract failed: %v", err)
	}
	s.Run("cd /home/user")
	s.Run("rm -rf restore proj")

	res = s.Run("gzip proj.tar.gz")
	if res.ExitCode != 0 {
		t.Fatalf("gzip failed: %s", res.Stderr)
	}
	if _, err := s.FS.Lookup(s.Cwd, "proj.tar.gz"); err == nil {
		t.Error("gzip should remove original")
	}
	if _, err := s.FS.Lookup(s.Cwd, "proj.tar.gz.gz"); err != nil {
		t.Error("gzip should create .gz")
	}
	s.Run("gunzip proj.tar.gz.gz")
	if _, err := s.FS.Lookup(s.Cwd, "proj.tar.gz"); err != nil {
		t.Error("gunzip should restore original name")
	}
}

func TestTextTools(t *testing.T) {
	s := run(t, "printf 'a:b:c\n d:e:f\n' > f")
	expect(t, out(t, s, "cut -d: -f2 f"), "b\ne\n")
	expect(t, out(t, s, "cut -c1-2 f"), "a:\n d\n")
	expect(t, out(t, s, "echo hello | tr a-z A-Z"), "HELLO\n")
	expect(t, out(t, s, "printf 'x1x\\n' | tr -d 1"), "xx\n")
	s.Run("printf 'line\n' > t.txt")
	res := s.Run("echo more | tee -a t.txt")
	if res.Stdout != "more\n" {
		t.Errorf("tee should pass through: %q", res.Stdout)
	}
	expect(t, s.FS.mustRead(t, "t.txt"), "line\nmore\n")
	// xargs
	s.Run("printf 'a.txt\nb.txt\n' > list")
	expect(t, out(t, s, "cat list | xargs echo rm"), "rm a.txt b.txt\n")
	s.Run("touch a.txt b.txt")
	s.Run("cat list | xargs rm")
	if _, err := s.FS.Lookup(s.Cwd, "a.txt"); err == nil {
		t.Error("xargs rm should remove files")
	}
}

func TestSortNumeric(t *testing.T) {
	s := run(t, "printf '10\n2\n33\n1\n' > n")
	expect(t, out(t, s, "sort n"), "1\n10\n2\n33\n")
	expect(t, out(t, s, "sort -n n"), "1\n2\n10\n33\n")
	expect(t, out(t, s, "sort -rn n"), "33\n10\n2\n1\n")
	s.Run("printf 'a\na\nb\n' > u")
	expect(t, out(t, s, "sort -u u"), "a\nb\n")
}

func TestCommandNotFound(t *testing.T) {
	s := NewShell()
	res := s.Run("frobnicate")
	if res.ExitCode != 127 || !strings.Contains(res.Stderr, "command not found") {
		t.Errorf("127 expected: %d %q", res.ExitCode, res.Stderr)
	}
}

func TestHistory(t *testing.T) {
	s := run(t, "echo 1", "echo 2")
	h := out(t, s, "history")
	if !strings.Contains(h, "1  echo 1") && !strings.Contains(h, "echo 1") {
		t.Errorf("history wrong: %q", h)
	}
}

func TestGlob(t *testing.T) {
	s := run(t, "touch a.txt b.txt c.md")
	expect(t, out(t, s, "ls *.txt"), "a.txt\nb.txt\n")
	res := s.Run("rm *.txt")
	if res.ExitCode != 0 {
		t.Errorf("glob rm failed: %s", res.Stderr)
	}
	if _, err := s.FS.Lookup(s.Cwd, "a.txt"); err == nil {
		t.Error("a.txt should be gone")
	}
}

func TestQuotes(t *testing.T) {
	s := NewShell()
	expect(t, out(t, s, `echo 'a  b'`), "a  b\n")
	expect(t, out(t, s, `echo "a  b"`), "a  b\n")
	expect(t, out(t, s, `echo a"b c"d`), "ab cd\n")
	expect(t, out(t, s, `echo it\'s`), "it's\n")
}

func TestSequences(t *testing.T) {
	s := NewShell()
	res := s.Run("echo a; echo b")
	if res.Stdout != "a\nb\n" {
		t.Errorf("sequence failed: %q", res.Stdout)
	}
}

func TestMan(t *testing.T) {
	s := NewShell()
	res := s.Run("man ls")
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "ls") {
		t.Errorf("man failed: %q", res.Stdout)
	}
	res = s.Run("man definitelynotacommand")
	if res.ExitCode == 0 {
		t.Error("man unknown should fail")
	}
}

// mustRead is a test helper reading a file in cwd.
func (fs *FS) mustRead(t *testing.T, name string) string {
	t.Helper()
	content, err := fs.ReadFile("/home/user", name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return content
}
