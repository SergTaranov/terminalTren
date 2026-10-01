package vshell

import (
	"path"
	"sort"
	"strings"
)

// hasWildcard reports whether the pattern contains glob metacharacters.
func hasWildcard(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '*', '?', '[':
			return true
		}
	}
	return false
}

// Glob expands a wildcard pattern against the filesystem relative to cwd.
// It returns matching absolute paths sorted by name; nil when nothing matches
// (callers then keep the literal pattern, like bash does).
func (fs *FS) Glob(cwd, pattern string) []string {
	abs := fs.AbsPath(cwd, pattern)
	var results []string

	var walk func(dir, rest string)
	walk = func(dir, rest string) {
		var seg, tail string
		if slash := indexSlash(rest); slash >= 0 {
			seg, tail = rest[:slash], rest[slash+1:]
		} else {
			seg = rest
		}
		entries, err := fs.ListDir("/", dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if seg == "" && tail != "" {
				continue // leading slash: handled by caller
			}
			// like bash without dotglob: * and ? do not match a leading dot
			if e.Name[0] == '.' && !strings.HasPrefix(seg, ".") {
				continue
			}
			if !matchSegment(seg, e.Name) {
				continue
			}
			full := path.Join(dir, e.Name)
			if tail == "" {
				results = append(results, full)
				continue
			}
			if e.Type == TypeDir {
				walk(full, tail)
			}
		}
	}

	rest := strings_trimPrefixSlash(abs)
	if rest == "" {
		return []string{"/"}
	}
	walk("/", rest)
	sort.Strings(results)
	return results
}

func strings_trimPrefixSlash(s string) string {
	if len(s) > 0 && s[0] == '/' {
		return s[1:]
	}
	return s
}

func indexSlash(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

// matchSegment matches one path component against a glob segment.
func matchSegment(pattern, name string) bool {
	return matchBytes([]rune(pattern), []rune(name))
}

func matchBytes(pattern, name []rune) bool {
	// classic recursive glob matcher supporting *, ? and [...] classes
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			for len(pattern) > 0 && pattern[0] == '*' {
				pattern = pattern[1:]
			}
			if len(pattern) == 0 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if matchBytes(pattern, name[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(name) == 0 {
				return false
			}
			pattern, name = pattern[1:], name[1:]
		case '[':
			if len(name) == 0 {
				return false
			}
			// find the closing ]: a ] first in the class is a literal member
			end := 1
			if end < len(pattern) && (pattern[end] == '!' || pattern[end] == '^') {
				end++
			}
			if end < len(pattern) && pattern[end] == ']' {
				end++
			}
			for end < len(pattern) && pattern[end] != ']' {
				end++
			}
			if end == len(pattern) {
				// unterminated class: literal '['
				if name[0] != '[' {
					return false
				}
				pattern, name = pattern[1:], name[1:]
				continue
			}
			if !matchClass(pattern[1:end], name[0]) {
				return false
			}
			pattern, name = pattern[end+1:], name[1:]
		default:
			if len(name) == 0 || pattern[0] != name[0] {
				return false
			}
			pattern, name = pattern[1:], name[1:]
		}
	}
	return len(name) == 0
}

func matchClass(class []rune, c rune) bool {
	negate := len(class) > 0 && (class[0] == '!' || class[0] == '^')
	if negate {
		class = class[1:]
	}
	matched := false
	for i := 0; i < len(class); i++ {
		if i+2 < len(class) && class[i+1] == '-' {
			if c >= class[i] && c <= class[i+2] {
				matched = true
			}
			i += 2
			continue
		}
		if class[i] == c {
			matched = true
		}
	}
	if negate {
		return !matched
	}
	return matched
}
