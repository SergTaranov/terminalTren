// Package curriculum defines the lesson data model and loads lessons from
// JSON files in the external lessons/ directory.
package curriculum

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"terminalTren/internal/vshell"
)

// Level is a difficulty tier.
type Level string

const (
	Basic    Level = "basic"
	Advanced Level = "advanced"
)

// Lesson is one training lesson: theory plus exercises.
type Lesson struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Level     Level           `json:"level"`
	Theory    []TheorySection `json:"theory"`
	Exercises []Exercise      `json:"exercises"`
	File      string          `json:"-"` // source file, set by the loader
}

// TheorySection is one theory page.
type TheorySection struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Exercise is one hands-on task with its sandbox setup and checks.
type Exercise struct {
	ID       string   `json:"id"`
	Task     string   `json:"task"`
	Setup    *Setup   `json:"setup"`
	Checks   []Check  `json:"checks"`
	Hints    []string `json:"hints"`
	Solution string   `json:"solution"`
}

// Setup seeds the virtual shell for an exercise.
type Setup struct {
	Cwd       string            `json:"cwd"`
	Dirs      []DirSpec         `json:"dirs"`
	Files     []FileSpec        `json:"files"`
	Processes []ProcessSpec     `json:"processes"`
	Units     []UnitSpec        `json:"units"`
	Web       map[string]string `json:"web"`
}

// DirSpec creates one directory.
type DirSpec struct {
	Path  string `json:"path"`
	Perms string `json:"perms"`
}

// FileSpec creates one file with content.
type FileSpec struct {
	Path         string `json:"path"`
	Content      string `json:"content"`
	Perms        string `json:"perms"`
	Owner        string `json:"owner"`
	MTimeDaysAgo int    `json:"mtime_days_ago"`
}

// ProcessSpec seeds a fake process for ps/kill.
type ProcessSpec struct {
	PID   int     `json:"pid"`
	User  string  `json:"user"`
	Cmd   string  `json:"cmd"`
	CPU   float64 `json:"cpu"`
	Mem   float64 `json:"mem"`
	State string  `json:"state"`
}

// UnitSpec seeds a systemd unit.
type UnitSpec struct {
	Name    string   `json:"name"`
	Desc    string   `json:"desc"`
	Active  string   `json:"active"`
	Enabled bool     `json:"enabled"`
	Logs    []string `json:"logs"`
}

// Check verifies exercise completion. Types:
//
//	fs_exists             — path exists in the virtual FS
//	fs_absent             — path does not exist
//	fs_content_contains   — file content contains value
//	fs_content_equals     — file content equals value (after trim)
//	fs_perm               — file permission bits equal octal value
//	cmd_used              — regex value matches some entered command
//	cwd_is                — current working directory equals value
//	output_contains       — last command output contains value
//	output_not_contains   — last command output does not contain value
type Check struct {
	Type  string `json:"type"`
	Path  string `json:"path"`
	Value string `json:"value"`
}

// Curriculum is the loaded set of lessons.
type Curriculum struct {
	Lessons []Lesson
}

// ByLevel returns lessons of one level in file order.
func (c *Curriculum) ByLevel(level Level) []Lesson {
	var out []Lesson
	for _, l := range c.Lessons {
		if l.Level == level {
			out = append(out, l)
		}
	}
	return out
}

// Lesson finds a lesson by id.
func (c *Curriculum) Lesson(id string) *Lesson {
	for i := range c.Lessons {
		if c.Lessons[i].ID == id {
			return &c.Lessons[i]
		}
	}
	return nil
}

// LoadDir reads lessons/<level>/*.json under root and validates them.
// A missing level directory is fine as long as at least one level has lessons.
func LoadDir(root string) (*Curriculum, error) {
	cur := &Curriculum{}
	seen := map[string]bool{}
	missingLevels := 0
	for _, level := range []Level{Basic, Advanced} {
		files, err := listJSONFiles(joinPath(root, string(level)))
		if err != nil {
			missingLevels++
			continue
		}
		for _, f := range files {
			l, err := loadLesson(f, level)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", f, err)
			}
			if seen[l.ID] {
				return nil, fmt.Errorf("duplicate lesson id %q", l.ID)
			}
			seen[l.ID] = true
			cur.Lessons = append(cur.Lessons, *l)
		}
	}
	if missingLevels == 2 {
		return nil, fmt.Errorf("no lesson directories found in %s (need lessons/basic/ and/or lessons/advanced/)", root)
	}
	sort.SliceStable(cur.Lessons, func(i, j int) bool {
		return cur.Lessons[i].File < cur.Lessons[j].File
	})
	if len(cur.Lessons) == 0 {
		return nil, fmt.Errorf("no lessons found in %s", root)
	}
	return cur, nil
}

// Apply seeds a fresh shell with the exercise setup.
func (su *Setup) Apply(s *vshell.Shell) error {
	if su == nil {
		return nil
	}
	fs := s.FS
	for _, d := range su.Dirs {
		perms := uint32(0o755)
		if d.Perms != "" {
			p, err := parseOctal(d.Perms)
			if err != nil {
				return fmt.Errorf("dir %s: %w", d.Path, err)
			}
			perms = p
		}
		if err := fs.MkdirAll("/", d.Path, perms, "user", "user"); err != nil {
			return fmt.Errorf("mkdir %s: %w", d.Path, err)
		}
	}
	for _, f := range su.Files {
		perms := uint32(0o644)
		if f.Perms != "" {
			p, err := parseOctal(f.Perms)
			if err != nil {
				return fmt.Errorf("file %s: %w", f.Path, err)
			}
			perms = p
		}
		owner := f.Owner
		if owner == "" {
			owner = "user"
		}
		if err := fs.MkdirAll("/", dirOf(f.Path), 0o755, "root", "root"); err != nil {
			return err
		}
		if err := fs.WriteFile("/", f.Path, f.Content, perms, owner, owner); err != nil {
			return fmt.Errorf("write %s: %w", f.Path, err)
		}
		if f.MTimeDaysAgo > 0 {
			if n, err := fs.Lookup("/", f.Path); err == nil {
				n.MTime = time.Now().AddDate(0, 0, -f.MTimeDaysAgo)
			}
		}
	}
	if su.Cwd != "" {
		if n, err := fs.Lookup("/", su.Cwd); err == nil && n.IsDir() {
			s.Cwd = su.Cwd
			s.Env["PWD"] = su.Cwd
		}
	}
	for _, p := range su.Processes {
		user := p.User
		if user == "" {
			user = "user"
		}
		s.Procs = append(s.Procs, &vshell.Process{
			PID: p.PID, User: user, Cmd: p.Cmd, CPU: p.CPU, Mem: p.Mem, State: p.State, Time: "0:01",
		})
	}
	for _, u := range su.Units {
		active := u.Active
		if active == "" {
			active = "active"
		}
		s.Units[u.Name] = &vshell.Unit{
			Name: u.Name, Desc: u.Desc, Active: active, Enabled: u.Enabled, Logs: u.Logs,
		}
	}
	for k, v := range su.Web {
		s.Web[k] = v
	}
	return nil
}

func dirOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return "/"
}
