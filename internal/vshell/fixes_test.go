package vshell

import (
	"strings"
	"testing"
)

// Regression tests pinning simulator behavior that once diverged from real
// bash/coreutils or crashed the trainer. Grouped by the defect class.

// ---- stability: panics and hangs ----

func TestSymlinkLoopDoesNotHang(t *testing.T) {
	s := run(t, "ln -s loop loop")
	res := s.Run("cat loop")
	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "Too many levels") {
		t.Errorf("symlink loop should fail with ELOOP: exit=%d stderr=%q", res.ExitCode, res.Stderr)
	}
	if strings.Contains(res.Stderr, "internal trainer error") {
		t.Errorf("symlink loop crashed the simulator: %q", res.Stderr)
	}
}

func TestAwkNegativeFieldNoPanic(t *testing.T) {
	// empty line: NF=0, so $(NF-1) used to index a.fields with a negative
	s := run(t, `printf 'a b\n\nc d\n' | awk '{print $(NF-1)}'`)
	res := s.Run("")
	_ = res
	last := s.History[len(s.History)-1]
	_ = last
	// rerun and inspect directly
	r := s.Run(`printf 'a b\n\nc d\n' | awk '{print $(NF-1)}'`)
	if strings.Contains(r.Stderr, "internal trainer error") {
		t.Fatalf("awk negative field panicked: %q", r.Stderr)
	}
	if r.ExitCode != 0 {
		t.Fatalf("awk negative field should not fail: %q", r.Stderr)
	}
}

func TestJournalctlNegativeLines(t *testing.T) {
	s := NewShell()
	s.Units["nginx.service"] = &Unit{Name: "nginx.service", Logs: []string{"l1", "l2", "l3"}}
	r := s.Run("journalctl -u nginx.service -n -2")
	if r.ExitCode == 0 || strings.Contains(r.Stderr, "internal trainer error") {
		t.Errorf("journalctl -n -2 should be a usage error, not a panic: exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	r = s.Run("journalctl -u nginx.service -n 2")
	if r.Stdout != "l2\nl3\n" {
		t.Errorf("journalctl -n 2 wrong output: %q", r.Stdout)
	}
}

func TestSedInPlaceSkipsMissingFile(t *testing.T) {
	s := run(t, "echo two > a.txt")
	r := s.Run("sed -i 's/two/TWO/' missing.txt a.txt")
	if r.ExitCode == 0 {
		t.Error("sed -i with a missing operand should fail")
	}
	// the good file IS processed; only the missing one errors
	if content, err := s.FS.ReadFile(s.Cwd, "a.txt"); err != nil || content != "TWO\n" {
		t.Errorf("a.txt must be processed, got %q (%v)", content, err)
	}
	if _, err := s.FS.Lookup(s.Cwd, "missing.txt"); err == nil {
		t.Error("missing.txt must not be conjured into existence")
	}
}

// ---- shell semantics ----

func TestPipelineRunsAllStages(t *testing.T) {
	s := NewShell()
	r := s.Run("cat missing.txt | wc -l")
	if r.Stdout != "0\n" {
		t.Errorf("cat missing | wc -l should print 0, got %q", r.Stdout)
	}
	if r.ExitCode != 0 {
		t.Errorf("pipeline status should be the last stage's, got %d", r.ExitCode)
	}
	r = s.Run("false | true")
	if r.ExitCode != 0 {
		t.Errorf("false | true should exit 0, got %d", r.ExitCode)
	}
}

func TestDollarQuestionUpdatesPerCommand(t *testing.T) {
	expect(t, out(t, NewShell(), "false; echo $?"), "1\n")
	expect(t, out(t, NewShell(), "true; echo $?"), "0\n")
}

func TestBackgroundSeparator(t *testing.T) {
	r := NewShell().Run("sleep 1 & echo hi")
	if r.Stdout != "hi\n" || r.ExitCode != 0 {
		t.Errorf("sleep 1 & echo hi: %q exit=%d stderr=%q", r.Stdout, r.ExitCode, r.Stderr)
	}
}

func TestAliasWithOperators(t *testing.T) {
	s := NewShell()
	s.Run("alias two='echo a; echo b'")
	expect(t, out(t, s, "two"), "a\nb\n")
}

func TestRedirectWithoutCommandCreatesFile(t *testing.T) {
	s := NewShell()
	r := s.Run("> empty.txt")
	if r.ExitCode != 0 {
		t.Fatalf("> file failed: %q", r.Stderr)
	}
	if _, err := s.FS.Lookup(s.Cwd, "empty.txt"); err != nil {
		t.Error("> file should create an empty file")
	}
}

func TestRedirectPermissionDenied(t *testing.T) {
	s := NewShell()
	r := s.Run("echo x > /etc/newfile")
	if r.ExitCode == 0 || !strings.Contains(r.Stderr, "Permission denied") {
		t.Errorf("user write into /etc must fail: exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestRootBypassesReadPermission(t *testing.T) {
	s := run(t, "echo secret > f", "chmod 000 f")
	r := s.Run("sudo cat f")
	if r.ExitCode != 0 || r.Stdout != "secret\n" {
		t.Errorf("sudo cat on a 000 file must succeed: exit=%d out=%q stderr=%q", r.ExitCode, r.Stdout, r.Stderr)
	}
}

func TestWriteThroughSymlink(t *testing.T) {
	s := run(t, "echo old > target", "ln -s target link")
	s.Run("echo new > link")
	if content, err := s.FS.ReadFile(s.Cwd, "target"); err != nil || content != "new\n" {
		t.Errorf("write via symlink must reach the target, target=%q (%v)", content, err)
	}
}

func TestSymlinkedParentDir(t *testing.T) {
	s := run(t, "mkdir real", "ln -s real slink")
	s.Run("touch slink/f.txt")
	if _, err := s.FS.Lookup(s.Cwd, "real/f.txt"); err != nil {
		t.Errorf("touch through a symlinked parent failed: %v", err)
	}
}

func TestRmDirectoryNeedsFlag(t *testing.T) {
	s := run(t, "mkdir emptydir")
	r := s.Run("rm emptydir")
	if r.ExitCode == 0 || !strings.Contains(r.Stderr, "Is a directory") {
		t.Errorf("rm without -r must refuse a directory: exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if _, err := s.FS.Lookup(s.Cwd, "emptydir"); err != nil {
		t.Error("rm without -r must not delete a directory")
	}
}

func TestMoveDirOntoFileFails(t *testing.T) {
	s := run(t, "touch f", "mkdir d")
	r := s.Run("mv d f")
	if r.ExitCode == 0 {
		t.Error("mv dir over file must fail")
	}
	if _, err := s.FS.Lookup(s.Cwd, "f"); err != nil {
		t.Error("mv dir over file destroyed the file")
	}
}

func TestGlobHidesDotfiles(t *testing.T) {
	expect(t, out(t, run(t, "touch .hidden a.txt"), "echo *"), "a.txt\n")
	expect(t, out(t, run(t, "touch .hidden"), "echo .*"), ".hidden\n")
}

// ---- text tools ----

func TestTailFromLineN(t *testing.T) {
	s := run(t, "printf '1\\n2\\n3\\n4\\n5\\n' > f")
	expect(t, out(t, s, "tail -n +3 f"), "3\n4\n5\n")
	expect(t, out(t, s, "head -n -2 f"), "1\n2\n3\n")
	expect(t, out(t, s, "printf '1\\n2\\n3\\n4\\n' | head -n -2"), "1\n2\n")
}

func TestSortKeyNumericModifier(t *testing.T) {
	in := "a:10\nb:2\n"
	s := NewShell()
	s.Run("printf 'a:10\\nb:2\\n' > f")
	expect(t, out(t, s, "sort -t: -k2n f"), "b:2\na:10\n")
	expect(t, out(t, s, "printf 'a:10\\nb:2\\n' | sort -t: -k2 -n"), "b:2\na:10\n")
	_ = in
}

func TestCutKeepsUndelimitedLines(t *testing.T) {
	r := NewShell().Run(`printf 'a:1\nplain\n' | cut -d: -f2`)
	if r.Stdout != "1\nplain\n" {
		t.Errorf("cut should print undelimited lines in full, got %q", r.Stdout)
	}
}

func TestPrintfReusesFormat(t *testing.T) {
	expect(t, out(t, NewShell(), `printf '%s\n' a b c`), "a\nb\nc\n")
	expect(t, out(t, NewShell(), `printf '%d-%d\n' 1 2 3 4`), "1-2\n3-4\n")
	expect(t, out(t, NewShell(), `printf '%d\n' 3.7`), "3\n")
}

func TestXargsNoRunIfEmpty(t *testing.T) {
	s := NewShell()
	r := s.Run("printf '' | xargs -r rm")
	if r.ExitCode != 0 || r.Stderr != "" {
		t.Errorf("xargs -r with empty input must do nothing: exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestSeqEqualWidth(t *testing.T) {
	expect(t, out(t, NewShell(), "seq -w 8 10"), "08\n09\n10\n")
}

func TestUniqOutputFile(t *testing.T) {
	s := run(t, "printf 'a\\na\\nb\\n' > in")
	s.Run("uniq in out")
	if content, err := s.FS.ReadFile(s.Cwd, "out"); err != nil || content != "a\nb\n" {
		t.Errorf("uniq in out: out=%q (%v)", content, err)
	}
}

func TestTrExtendsShortSet2(t *testing.T) {
	r := NewShell().Run(`echo abc | tr ab X`)
	if r.Stdout != "XXc\n" {
		t.Errorf("tr ab X on abc should give XXc, got %q", r.Stdout)
	}
}

// ---- regex: BRE support ----

func TestSedBREGroups(t *testing.T) {
	expect(t, out(t, NewShell(), `echo hello | sed 's/\(hell\)o/[\1]/'`), "[hell]\n")
	expect(t, out(t, NewShell(), `echo aaa | sed 's/a\{2\}/X/'`), "Xa\n")
}

func TestGrepBRE(t *testing.T) {
	s := run(t, `printf 'C++ rocks\nC is fine\n' > f`)
	r := s.Run("grep 'C++' f")
	if r.Stdout != "C++ rocks\n" {
		t.Errorf(`grep 'C++' must match literally, got %q`, r.Stdout)
	}
	r = s.Run("grep 'f\\+' f")
	if r.Stdout != "C is fine\n" {
		t.Errorf(`grep BRE \\+ must mean one-or-more, got %q`, r.Stdout)
	}
}

func TestGrepErrorExitCode2(t *testing.T) {
	r := NewShell().Run("grep x nosuchfile")
	if r.ExitCode != 2 {
		t.Errorf("grep on unreadable file should exit 2, got %d", r.ExitCode)
	}
}

// ---- awk ----

func TestAwkGsubSubMatch(t *testing.T) {
	expect(t, out(t, NewShell(), `echo banana | awk '{gsub(/a/, "o"); print}'`), "bonono\n")
	expect(t, out(t, NewShell(), `echo banana | awk '{sub(/a/, "o"); print}'`), "bonana\n")
	expect(t, out(t, NewShell(), `echo abc | awk '{match($0, /b/); print RSTART, RLENGTH}'`), "2 1\n")
}

func TestAwkEndRunsAfterExit(t *testing.T) {
	r := NewShell().Run(`printf '5\nx\n' | awk '{if ($0 == "x") exit} END{print NR}'`)
	if r.Stdout != "2\n" {
		t.Errorf("END must run after exit, got %q (stderr: %q, exit %d)", r.Stdout, r.Stderr, r.ExitCode)
	}
}

func TestAwkStringVsNumberComparison(t *testing.T) {
	s := NewShell()
	r := s.Run(`printf 'dev x\n' | awk '$1 < 100 {print "matched"}'`)
	if r.Stdout != "" {
		t.Errorf("'dev' < 100 must be a string comparison (false), got %q", r.Stdout)
	}
	r = s.Run(`printf '50 x\n' | awk '$1 < 100 {print "matched"}'`)
	if r.Stdout != "matched\n" {
		t.Errorf("numeric field < 100 should match, got %q", r.Stdout)
	}
}

func TestAwkEmptyLineNF(t *testing.T) {
	r := NewShell().Run(`printf '\n' | awk -F: '{print NF}'`)
	if r.Stdout != "0\n" {
		t.Errorf("empty line with -F: must give NF=0, got %q", r.Stdout)
	}
}

func TestAwkNextSkipsRules(t *testing.T) {
	r := NewShell().Run(`printf '#c\nok\n' | awk '/^#/ {next} {print}'`)
	if r.Stdout != "ok\n" {
		t.Errorf("next must skip remaining rules for the record, got %q", r.Stdout)
	}
}

func TestAwkToNumPrefix(t *testing.T) {
	r := NewShell().Run(`printf '12kg\n' | awk '$1 + 0 {print "truthy"}'`)
	if r.Stdout != "truthy\n" {
		t.Errorf("awk must coerce '12kg' to 12, got %q", r.Stdout)
	}
}

func TestAwkFSEscape(t *testing.T) {
	r := NewShell().Run(`printf 'a\tb\n' | awk -F'\t' '{print $2}'`)
	if r.Stdout != "b\n" {
		t.Errorf("awk -F'\\t' should split on TAB, got %q", r.Stdout)
	}
}

// ---- archives ----

func TestTarMemberPaths(t *testing.T) {
	s := run(t, "mkdir -p docs", "echo x > docs/f.txt")
	s.Run("tar -cf a.tar docs/f.txt")
	listing := out(t, s, "tar -tf a.tar")
	if !strings.Contains(listing, "docs/f.txt") {
		t.Errorf("tar should keep the operand path docs/f.txt, got %q", listing)
	}
	s.Run("tar -xf a.tar")
	if _, err := s.FS.Lookup(s.Cwd, "docs/f.txt"); err != nil {
		t.Errorf("extract should restore docs/f.txt: %v", err)
	}
}

func TestTarGzipAutodetect(t *testing.T) {
	s := run(t, "mkdir proj", "echo x > proj/f.txt")
	s.Run("tar -czf a.tar.gz proj")
	r := s.Run("tar -tf a.tar.gz")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "proj/f.txt") {
		t.Errorf("tar -tf on .tar.gz without -z must autodetect: %q %q", r.Stdout, r.Stderr)
	}
}

func TestGzipKeepAndStdout(t *testing.T) {
	s := run(t, "echo data > f")
	s.Run("gzip -k f")
	if _, err := s.FS.Lookup(s.Cwd, "f"); err != nil {
		t.Error("gzip -k must keep the original")
	}
	if _, err := s.FS.Lookup(s.Cwd, "f.gz"); err != nil {
		t.Error("gzip -k must create f.gz")
	}
	r := s.Run("gzip -c f > f2.gz")
	if r.ExitCode != 0 {
		t.Fatalf("gzip -c failed: %q", r.Stderr)
	}
	if _, err := s.FS.Lookup(s.Cwd, "f"); err != nil {
		t.Error("gzip -c must keep the original")
	}
}

// ---- misc commands ----

func TestEnvAssignments(t *testing.T) {
	s := NewShell()
	r := s.Run("env FOO=bar")
	if !strings.Contains(r.Stdout, "FOO=bar") {
		t.Errorf("env FOO=bar should print the assignment, got %q", r.Stdout)
	}
	if _, ok := s.Env["FOO"]; ok {
		t.Error("env must not leak assignments into the shell")
	}
	r = s.Run("env FOO=bar true")
	if r.ExitCode != 0 || r.Stdout != "" {
		t.Errorf("env VAR=x cmd must run cmd silently: %q %q", r.Stdout, r.Stderr)
	}
}

func TestChmodBigX(t *testing.T) {
	s := run(t, "touch plain", "mkdir dir")
	s.Run("chmod a+X plain dir")
	if pn, _ := s.FS.Lookup(s.Cwd, "plain"); pn.Perms&0o111 != 0 {
		t.Errorf("chmod a+X must not make a plain file executable: %o", pn.Perms)
	}
	if dn, _ := s.FS.Lookup(s.Cwd, "dir"); dn.Perms&0o111 == 0 {
		t.Errorf("chmod a+X must add x to directories: %o", dn.Perms)
	}
}

func TestChmodFollowsSymlink(t *testing.T) {
	s := run(t, "echo x > f", "ln -s f l")
	s.Run("chmod 600 l")
	if fn, _ := s.FS.Lookup(s.Cwd, "f"); fn.Perms != 0o600 {
		t.Errorf("chmod via symlink must change the target: %o", fn.Perms)
	}
}

func TestScriptLocalScope(t *testing.T) {
	s := run(t, "printf 'f() {\\n  local x=inner\\n  echo $x\\n}\\nx=outer\\nf\\necho $x\\n' > s.sh")
	expect(t, out(t, s, "bash s.sh"), "inner\nouter\n")
}

func TestScriptDollarAt(t *testing.T) {
	s := run(t, "printf 'echo \"$@\"\\n' > s.sh")
	expect(t, out(t, s, "bash s.sh a b"), "a b\n")
}

func TestScriptFunctionsDoNotLeak(t *testing.T) {
	s := run(t, "printf 'myfn() { echo hi; }\\n' > s.sh")
	s.Run("bash s.sh")
	r := s.Run("myfn")
	if r.ExitCode != 127 {
		t.Errorf("functions from a child script must not leak, got exit %d", r.ExitCode)
	}
}

func TestIfFalseStatus(t *testing.T) {
	s := run(t, "printf 'false\\nif false; then\\n  echo x\\nfi\\n' > s.sh")
	r := s.Run("bash s.sh")
	if r.ExitCode != 0 {
		t.Errorf("if with no branch taken must exit 0, got %d (%q)", r.ExitCode, r.Stderr)
	}
}

func TestShDashC(t *testing.T) {
	expect(t, out(t, NewShell(), `sh -c 'echo works'`), "works\n")
}

func TestExpandWordsQuoted(t *testing.T) {
	s := run(t, "printf 'for x in \"a b\" c; do\\n  echo [$x]\\ndone\\n' > s.sh")
	expect(t, out(t, s, "bash s.sh"), "[a b]\n[c]\n")
}

func TestDuOnFile(t *testing.T) {
	s := run(t, "echo hello > f")
	r := s.Run("du f")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "f") {
		t.Errorf("du on a file should print it, got %q %q", r.Stdout, r.Stderr)
	}
}
