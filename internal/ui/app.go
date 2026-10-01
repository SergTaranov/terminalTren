package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"terminalTren/internal/curriculum"
	"terminalTren/internal/trainer"
)

type screen int

const (
	screenHome screen = iota
	screenBrowser
	screenLesson
	screenDone
)

// App is the root bubbletea model.
type App struct {
	cur     *curriculum.Curriculum
	prog    *trainer.Progress
	screen  screen
	menuIdx int
	level   curriculum.Level

	browserIdx int
	lesson     *lessonModel
	nextLesson *curriculum.Lesson // for the done screen
	lastLevel  curriculum.Level

	width, height int
}

// NewApp creates the root model.
func NewApp(cur *curriculum.Curriculum, prog *trainer.Progress) *App {
	return &App{cur: cur, prog: prog}
}

// Init implements tea.Model.
func (a *App) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		if a.lesson != nil {
			a.lesson.resize(msg.Width, msg.Height)
		}
		return a, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			a.prog.Save()
			return a, tea.Quit
		}
	}
	switch a.screen {
	case screenHome:
		return a.updateHome(msg)
	case screenBrowser:
		return a.updateBrowser(msg)
	case screenLesson:
		return a.updateLesson(msg)
	case screenDone:
		return a.updateDone(msg)
	}
	return a, nil
}

func (a *App) updateHome(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return a, nil
	}
	items := a.homeItems()
	switch key.String() {
	case "q":
		a.prog.Save()
		return a, tea.Quit
	case "up", "k":
		if a.menuIdx > 0 {
			a.menuIdx--
		}
	case "down", "j":
		if a.menuIdx < len(items)-1 {
			a.menuIdx++
		}
	case "enter":
		switch a.menuIdx {
		case 0:
			a.level = curriculum.Basic
			a.screen = screenBrowser
			a.browserIdx = 0
		case 1:
			a.level = curriculum.Advanced
			a.screen = screenBrowser
			a.browserIdx = 0
		case 2:
			a.prog.Save()
			return a, tea.Quit
		}
	}
	return a, nil
}

func (a *App) homeItems() []string {
	return []string{"Базовый уровень", "Продвинутый уровень", "Выход"}
}

func (a *App) updateBrowser(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return a, nil
	}
	lessons := a.cur.ByLevel(a.level)
	switch key.String() {
	case "esc", "q":
		a.screen = screenHome
	case "up", "k":
		if a.browserIdx > 0 {
			a.browserIdx--
		}
	case "down", "j":
		if a.browserIdx < len(lessons)-1 {
			a.browserIdx++
		}
	case "enter":
		if len(lessons) == 0 {
			return a, nil
		}
		lesson := lessons[a.browserIdx]
		lm, err := newLessonModel(&lesson, a.prog, a.width, a.height)
		if err != nil {
			return a, nil
		}
		a.lesson = lm
		a.screen = screenLesson
		return a, textinputFocus(lm)
	}
	return a, nil
}

func (a *App) updateLesson(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := a.lesson.Update(msg)
	a.lesson = model.(*lessonModel)
	if a.lesson.closed {
		a.lesson = nil
		a.screen = screenBrowser
		return a, nil
	}
	if a.lesson.finishedLesson {
		a.nextLesson = a.findNextLesson(a.lesson.lesson)
		a.lastLevel = a.lesson.lesson.Level
		a.lesson = nil
		a.screen = screenDone
		return a, nil
	}
	return a, cmd
}

// findNextLesson returns the lesson following l within its level.
func (a *App) findNextLesson(l *curriculum.Lesson) *curriculum.Lesson {
	lessons := a.cur.ByLevel(l.Level)
	for i := range lessons {
		if lessons[i].ID == l.ID && i+1 < len(lessons) {
			return &lessons[i+1]
		}
	}
	return nil
}

func (a *App) updateDone(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return a, nil
	}
	switch key.String() {
	case "enter":
		if a.nextLesson != nil {
			lm, err := newLessonModel(a.nextLesson, a.prog, a.width, a.height)
			if err == nil {
				a.lesson = lm
				a.screen = screenLesson
				return a, textinputFocus(lm)
			}
		}
		a.screen = screenBrowser
	case "esc":
		a.screen = screenBrowser
	}
	return a, nil
}

// View implements tea.Model.
func (a *App) View() string {
	var b strings.Builder
	switch a.screen {
	case screenHome:
		b.WriteString(a.viewHome())
	case screenBrowser:
		b.WriteString(a.viewBrowser())
	case screenLesson:
		if a.lesson != nil {
			b.WriteString(a.lesson.View())
		}
	case screenDone:
		b.WriteString(a.viewDone())
	}
	if b.Len() == 0 {
		return ""
	}
	// pad/crop to terminal height to avoid flicker
	lines := strings.Split(b.String(), "\n")
	if len(lines) > a.height && a.height > 0 {
		lines = lines[:a.height]
	}
	return strings.Join(lines, "\n")
}

func (a *App) viewHome() string {
	var b strings.Builder
	done, total := a.prog.Stats(a.cur)
	b.WriteString("\n\n")
	title := styleTitle.Render(" terminalTren ")
	b.WriteString("  " + title + "\n")
	b.WriteString("  " + styleSubtitle.Render("тренажёр командной строки Linux") + "\n\n")
	b.WriteString(fmt.Sprintf("  Прогресс: %d из %d уроков завершено\n\n", done, total))
	for i, item := range a.homeItems() {
		st := styleMenuItem
		if i == a.menuIdx {
			st = styleMenuSel
		}
		b.WriteString(st.Render(item) + "\n")
	}
	b.WriteString("\n  " + styleSubtitle.Render("↑/↓ выбор · Enter открыть · q выход") + "\n")
	return b.String()
}

func (a *App) viewBrowser() string {
	levelTitle := "Базовый уровень"
	if a.level == curriculum.Advanced {
		levelTitle = "Продвинутый уровень"
	}
	var b strings.Builder
	b.WriteString(styleHeader.Render(styleBold.Render(levelTitle)) + "\n")
	lessons := a.cur.ByLevel(a.level)
	for i, l := range lessons {
		solved := a.prog.LessonSolvedCount(&l)
		mark := stylePending.Render("○")
		if a.prog.IsLessonDone(&l) {
			mark = styleDone.Render("✓")
		}
		title := fmt.Sprintf("%s %s", mark, l.Title)
		st := styleMenuItem
		if i == a.browserIdx {
			st = styleMenuSel
		}
		counter := styleSubtitle.Render(fmt.Sprintf(" %d/%d", solved, len(l.Exercises)))
		if i == a.browserIdx {
			counter = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).
				Render(fmt.Sprintf(" %d/%d", solved, len(l.Exercises)))
		}
		b.WriteString(st.Render(title) + counter + "\n")
	}
	b.WriteString("\n" + styleStatusBar.Render("↑/↓ выбор · Enter начать урок · Esc назад"))
	return b.String()
}

func (a *App) viewDone() string {
	var b strings.Builder
	b.WriteString("\n\n  " + styleSuccess.Render("✓ Урок завершён!") + "\n\n")
	if a.nextLesson != nil {
		b.WriteString("  Следующий урок: " + styleBold.Render(a.nextLesson.Title) + "\n")
		b.WriteString("\n  " + styleStatusBar.Render("Enter — следующий урок · Esc — к списку уроков") + "\n")
	} else {
		b.WriteString("  Это был последний урок уровня. Отличная работа!\n")
		b.WriteString("\n  " + styleStatusBar.Render("Enter/Esc — к списку уроков") + "\n")
	}
	return b.String()
}
