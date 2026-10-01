// Package trainer drives exercise sessions: it runs commands in the virtual
// shell, checks exercise completion and persists progress.
package trainer

import (
	"fmt"
	"regexp"
	"strings"

	"terminalTren/internal/curriculum"
	"terminalTren/internal/vshell"
)

// Evaluate checks all conditions and returns (allPassed, failed descriptions).
func Evaluate(checks []curriculum.Check, s *vshell.Shell, last *vshell.Result) (bool, []string) {
	var failed []string
	for _, ch := range checks {
		ok, desc := evalCheck(ch, s, last)
		if !ok {
			failed = append(failed, desc)
		}
	}
	return len(failed) == 0, failed
}

func evalCheck(ch curriculum.Check, s *vshell.Shell, last *vshell.Result) (bool, string) {
	switch ch.Type {
	case "fs_exists":
		_, err := s.FS.LookupNoFollow(s.Cwd, ch.Path)
		return err == nil, fmt.Sprintf("не существует: %s", ch.Path)
	case "fs_absent":
		_, err := s.FS.LookupNoFollow(s.Cwd, ch.Path)
		return err != nil, fmt.Sprintf("существует: %s", ch.Path)
	case "fs_content_contains":
		content, err := s.FS.ReadFile(s.Cwd, ch.Path)
		if err != nil {
			return false, fmt.Sprintf("не читается: %s", ch.Path)
		}
		return strings.Contains(content, ch.Value), fmt.Sprintf("%s не содержит %q", ch.Path, ch.Value)
	case "fs_content_equals":
		content, err := s.FS.ReadFile(s.Cwd, ch.Path)
		if err != nil {
			return false, fmt.Sprintf("не читается: %s", ch.Path)
		}
		return strings.TrimSpace(content) == strings.TrimSpace(ch.Value),
			fmt.Sprintf("содержимое %s != %q", ch.Path, ch.Value)
	case "fs_perm":
		n, err := s.FS.LookupNoFollow(s.Cwd, ch.Path)
		if err != nil {
			return false, fmt.Sprintf("не существует: %s", ch.Path)
		}
		return fmt.Sprintf("%03o", n.Perms&0o777) == normalizeMode(ch.Value),
			fmt.Sprintf("права %s != %s", ch.Path, ch.Value)
	case "cmd_used":
		re, err := regexp.Compile(ch.Value)
		if err != nil {
			return false, "некорректный шаблон проверки"
		}
		for _, h := range s.History {
			// a compound entry (a && b; c) counts if any of its parts matches
			for _, seg := range vshell.SplitSegments(h) {
				if re.MatchString(strings.TrimSpace(seg)) {
					return true, "нужная команда ещё не вводилась"
				}
			}
		}
		return false, "нужная команда ещё не вводилась"
	case "cwd_is":
		return s.Cwd == ch.Value, fmt.Sprintf("текущий каталог != %s", ch.Value)
	case "output_contains":
		if last == nil {
			return false, "нет вывода"
		}
		return strings.Contains(last.Stdout+last.Stderr, ch.Value),
			fmt.Sprintf("вывод не содержит %q", ch.Value)
	case "output_not_contains":
		if last == nil {
			return false, "нет вывода"
		}
		return !strings.Contains(last.Stdout+last.Stderr, ch.Value),
			fmt.Sprintf("вывод содержит %q", ch.Value)
	}
	return false, fmt.Sprintf("неизвестный тип проверки: %s", ch.Type)
}

func normalizeMode(s string) string {
	s = strings.TrimSpace(s)
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}
