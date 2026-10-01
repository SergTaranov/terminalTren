package trainer

import (
	"fmt"

	"terminalTren/internal/curriculum"
	"terminalTren/internal/vshell"
)

// Session is the state of a lesson in progress.
type Session struct {
	Lesson     *curriculum.Lesson
	ExIndex    int
	Shell      *vshell.Shell
	HintsShown int
	SolvedList []bool // per exercise
	lastResult *vshell.Result
	justSolved bool
}

// StartLesson prepares a session with the first exercise sandbox.
func StartLesson(l *curriculum.Lesson) (*Session, error) {
	s := &Session{
		Lesson:     l,
		SolvedList: make([]bool, len(l.Exercises)),
	}
	if err := s.loadExercise(0); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Session) loadExercise(i int) error {
	s.Shell = vshell.NewShell()
	s.HintsShown = 0
	s.justSolved = false
	ex := &s.Lesson.Exercises[i]
	if ex.Setup != nil {
		if err := ex.Setup.Apply(s.Shell); err != nil {
			return fmt.Errorf("упражнение %s: %w", ex.ID, err)
		}
	}
	return nil
}

// Exercise returns the current exercise.
func (s *Session) Exercise() *curriculum.Exercise {
	return &s.Lesson.Exercises[s.ExIndex]
}

// Execute runs a command line and checks completion afterwards.
func (s *Session) Execute(line string) *vshell.Result {
	res := s.Shell.Run(line)
	s.lastResult = res
	s.justSolved = false
	if done, _ := Evaluate(s.Exercise().Checks, s.Shell, res); done {
		s.SolvedList[s.ExIndex] = true
		s.justSolved = true
	}
	return res
}

// JustSolved reports whether the last Execute solved the exercise.
func (s *Session) JustSolved() bool { return s.justSolved }

// IsSolved reports whether the current exercise is complete.
func (s *Session) IsSolved() bool { return s.SolvedList[s.ExIndex] }

// NextExercise advances to the next exercise, resetting the sandbox.
// It returns false when the lesson is over.
func (s *Session) NextExercise() (bool, error) {
	if s.ExIndex+1 >= len(s.Lesson.Exercises) {
		return false, nil
	}
	s.ExIndex++
	return true, s.loadExercise(s.ExIndex)
}

// Reset re-seeds the current exercise sandbox.
func (s *Session) Reset() error {
	solved := s.SolvedList[s.ExIndex]
	if err := s.loadExercise(s.ExIndex); err != nil {
		return err
	}
	s.SolvedList[s.ExIndex] = solved
	return nil
}

// ShowHint reveals the next hint, if any remain.
func (s *Session) ShowHint() (string, bool) {
	hints := s.Exercise().Hints
	if s.HintsShown >= len(hints) {
		return "", false
	}
	h := hints[s.HintsShown]
	s.HintsShown++
	return h, true
}

// LessonDone reports whether every exercise is solved.
func (s *Session) LessonDone() bool {
	for _, ok := range s.SolvedList {
		if !ok {
			return false
		}
	}
	return true
}

// SolvedCount returns the number of solved exercises.
func (s *Session) SolvedCount() int {
	n := 0
	for _, ok := range s.SolvedList {
		if ok {
			n++
		}
	}
	return n
}

// LastResult returns the result of the most recent command.
func (s *Session) LastResult() *vshell.Result { return s.lastResult }
