package trainer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"terminalTren/internal/curriculum"
)

// Progress persists exercise completion across runs.
type Progress struct {
	Lessons map[string]*LessonProgress `json:"lessons"`
	path    string
}

// LessonProgress records solved exercise ids of one lesson.
type LessonProgress struct {
	Exercises  map[string]bool `json:"exercises"`
	FinishedAt string          `json:"finished_at,omitempty"`
}

// LoadProgress reads progress.json from the user config dir, creating an
// empty store when absent. A corrupted file is reported to stderr instead
// of being silently discarded.
func LoadProgress() *Progress {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	path := filepath.Join(dir, "terminalTren", "progress.json")
	p := &Progress{Lessons: map[string]*LessonProgress{}, path: path}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, p); err != nil {
			fmt.Fprintf(os.Stderr, "terminalTren: файл прогресса повреждён (%v), сохранённая копия не тронута\n", err)
			p.Lessons = map[string]*LessonProgress{}
		}
		if p.Lessons == nil {
			p.Lessons = map[string]*LessonProgress{}
		}
	}
	return p
}

// Save writes progress to disk atomically (tmp file + rename), so a crash
// mid-write cannot corrupt the stored file.
func (p *Progress) Save() error {
	if p.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p.path)
}

func (p *Progress) lesson(id string) *LessonProgress {
	lp, ok := p.Lessons[id]
	if !ok {
		lp = &LessonProgress{Exercises: map[string]bool{}}
		p.Lessons[id] = lp
	}
	return lp
}

// MarkExercise records an exercise as solved.
func (p *Progress) MarkExercise(lessonID, exID string) {
	p.lesson(lessonID).Exercises[exID] = true
}

// MarkLessonFinished stamps the finish time when all exercises are solved.
func (p *Progress) MarkLessonFinished(l *curriculum.Lesson, solved int) {
	lp := p.lesson(l.ID)
	if solved >= len(l.Exercises) && lp.FinishedAt == "" {
		lp.FinishedAt = time.Now().Format("2006-01-02 15:04")
	}
}

// IsExerciseSolved reports saved state for one exercise.
func (p *Progress) IsExerciseSolved(lessonID, exID string) bool {
	lp, ok := p.Lessons[lessonID]
	if !ok {
		return false
	}
	return lp.Exercises[exID]
}

// LessonSolvedCount returns how many exercises of the lesson are solved.
func (p *Progress) LessonSolvedCount(l *curriculum.Lesson) int {
	lp, ok := p.Lessons[l.ID]
	if !ok {
		return 0
	}
	n := 0
	for _, ex := range l.Exercises {
		if lp.Exercises[ex.ID] {
			n++
		}
	}
	return n
}

// IsLessonDone reports whether the whole lesson is finished.
func (p *Progress) IsLessonDone(l *curriculum.Lesson) bool {
	return p.LessonSolvedCount(l) >= len(l.Exercises)
}

// NextLesson returns the first unfinished lesson of the level.
func (p *Progress) NextLesson(cur *curriculum.Curriculum, level curriculum.Level) *curriculum.Lesson {
	for i := range cur.ByLevel(level) {
		l := cur.ByLevel(level)[i]
		if !p.IsLessonDone(&l) {
			return &l
		}
	}
	return nil
}

// Stats returns (finished lessons, total lessons) across the curriculum.
func (p *Progress) Stats(cur *curriculum.Curriculum) (int, int) {
	done := 0
	for i := range cur.Lessons {
		if p.IsLessonDone(&cur.Lessons[i]) {
			done++
		}
	}
	return done, len(cur.Lessons)
}
