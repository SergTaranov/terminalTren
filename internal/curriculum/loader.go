package curriculum

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// listJSONFiles returns sorted *.json paths in dir.
func listJSONFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("directory not found")
		}
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	return files, nil
}

func loadLesson(file string, level Level) (*Lesson, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var l Lesson
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, err
	}
	if l.ID == "" {
		return nil, fmt.Errorf("missing id")
	}
	if l.Title == "" {
		return nil, fmt.Errorf("missing title")
	}
	if len(l.Exercises) == 0 {
		return nil, fmt.Errorf("no exercises")
	}
	exIDs := map[string]bool{}
	for i := range l.Exercises {
		ex := &l.Exercises[i]
		if ex.Task == "" {
			return nil, fmt.Errorf("exercise %d: missing task", i+1)
		}
		if len(ex.Checks) == 0 {
			return nil, fmt.Errorf("exercise %d: no checks", i+1)
		}
		if ex.ID == "" {
			return nil, fmt.Errorf("exercise %d: missing id", i+1)
		}
		if exIDs[ex.ID] {
			return nil, fmt.Errorf("duplicate exercise id %q", ex.ID)
		}
		exIDs[ex.ID] = true
		for _, ch := range ex.Checks {
			if !knownCheckTypes[ch.Type] {
				return nil, fmt.Errorf("exercise %s: unknown check type %q", ex.ID, ch.Type)
			}
		}
		if err := validateSetup(ex.Setup); err != nil {
			return nil, fmt.Errorf("exercise %s: %w", ex.ID, err)
		}
	}
	if l.Level == "" {
		l.Level = level
	}
	l.File = filepath.Base(file)
	return &l, nil
}

// knownCheckTypes mirrors the types understood by trainer.Evaluate.
var knownCheckTypes = map[string]bool{
	"fs_exists":           true,
	"fs_absent":           true,
	"fs_content_contains": true,
	"fs_content_equals":   true,
	"fs_perm":             true,
	"cmd_used":            true,
	"cwd_is":              true,
	"output_contains":     true,
	"output_not_contains": true,
}

// validateSetup catches bad perms/mtime values at load time instead of
// failing (or silently misbehaving) at exercise start.
func validateSetup(su *Setup) error {
	if su == nil {
		return nil
	}
	for _, d := range su.Dirs {
		if _, err := parseOctal(d.Perms); err != nil {
			return fmt.Errorf("dir %s: invalid octal perms %q", d.Path, d.Perms)
		}
	}
	for _, f := range su.Files {
		if _, err := parseOctal(f.Perms); err != nil {
			return fmt.Errorf("file %s: invalid octal perms %q", f.Path, f.Perms)
		}
	}
	return nil
}

func parseOctal(s string) (uint32, error) {
	if s == "" {
		return 0, nil
	}
	v, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, err
	}
	return uint32(v), nil
}

func joinPath(parts ...string) string {
	return filepath.Join(parts...)
}
