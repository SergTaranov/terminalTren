package trainer

import (
	"os"
	"path/filepath"
	"testing"

	"terminalTren/internal/curriculum"
)

// loadTestCurriculum loads the real lesson files next to the module root.
func loadTestCurriculum(t *testing.T) *curriculum.Curriculum {
	t.Helper()
	for _, dir := range []string{"lessons", filepath.Join("..", "..", "lessons")} {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			cur, err := curriculum.LoadDir(dir)
			if err != nil {
				t.Fatalf("load %s: %v", dir, err)
			}
			return cur
		}
	}
	t.Fatal("lessons directory not found")
	return nil
}

// TestLessonSolutions replays every lesson's solution for every exercise and
// asserts the checks pass. This validates lesson content end-to-end.
func TestLessonSolutions(t *testing.T) {
	cur := loadTestCurriculum(t)
	for _, lesson := range cur.Lessons {
		lesson := lesson
		t.Run(lesson.ID, func(t *testing.T) {
			sess, err := StartLesson(&lesson)
			if err != nil {
				t.Fatalf("start lesson: %v", err)
			}
			for i, ex := range lesson.Exercises {
				if ex.Solution == "" {
					t.Fatalf("exercise %s: empty solution", ex.ID)
				}
				res := sess.Execute(ex.Solution)
				if res.ExitCode != 0 && res.Stderr != "" {
					t.Logf("exercise %s solution stderr: %s", ex.ID, res.Stderr)
				}
				if !sess.IsSolved() {
					_, failed := Evaluate(ex.Checks, sess.Shell, sess.LastResult())
					t.Errorf("exercise %s: solution %q did not pass checks (exit=%d stderr=%q) failed=%v",
						ex.ID, ex.Solution, res.ExitCode, res.Stderr, failed)
				}
				if i+1 < len(lesson.Exercises) {
					if ok, err := sess.NextExercise(); !ok || err != nil {
						t.Fatalf("advance: %v", err)
					}
				}
			}
			if !sess.LessonDone() {
				t.Error("lesson should be complete after all solutions")
			}
		})
	}
}
