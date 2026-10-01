package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"terminalTren/internal/curriculum"
	"terminalTren/internal/trainer"
)

type lessonTab int

const (
	tabTheory lessonTab = iota
	tabPractice
)

const maxOutputLines = 400

// lessonModel drives the lesson screen: theory reader and the practice
// terminal with exercise checking.
type lessonModel struct {
	session *trainer.Session
	prog    *trainer.Progress
	lesson  *curriculum.Lesson

	tab            lessonTab
	theoryVP       viewport.Model
	outVP          viewport.Model
	input          textinput.Model
	output         []string
	awaitNext      bool // waiting for Enter after exercise solved
	historyIdx     int
	historyBuf     string
	closed         bool
	finishedLesson bool // whole lesson is complete
	width          int
	height         int
}

// newLessonModel builds the lesson screen.
func newLessonModel(l *curriculum.Lesson, prog *trainer.Progress, width, height int) (*lessonModel, error) {
	sess, err := trainer.StartLesson(l)
	if err != nil {
		return nil, err
	}
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 0

	theory := buildTheoryDoc(l)
	m := &lessonModel{
		session: sess,
		prog:    prog,
		lesson:  l,
		input:   ti,
	}
	m.theoryVP = viewport.New(78, 20)
	m.outVP = viewport.New(78, 10)
	m.theoryVP.SetContent(renderMarkdown(theory, 76))
	m.input.Focus()
	m.appendOut(styleDimLine("Введите help — список команд, man <команда> — справка."))
	m.resize(width, height)
	return m, nil
}

func textinputFocus(m *lessonModel) tea.Cmd {
	return m.input.Focus()
}

// Init implements tea.Model.
func (m *lessonModel) Init() tea.Cmd { return nil }

func buildTheoryDoc(l *curriculum.Lesson) string {
	var b strings.Builder
	b.WriteString("# " + l.Title + "\n\n")
	for _, sec := range l.Theory {
		b.WriteString("## " + sec.Title + "\n\n" + sec.Body + "\n\n")
	}
	return b.String()
}

func (m *lessonModel) resize(width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	m.width, m.height = width, height
	inner := width - 4
	if inner < 20 {
		inner = 20
	}
	m.theoryVP.Width = inner
	m.theoryVP.Height = height - 4
	m.outVP.Width = inner
	m.refreshOutputVP()
}

// layout helpers -----------------------------------------------------------

func min(a, b int) int {
	if a < b {
		return a
	}
	return b

}

// Update implements tea.Model.
func (m *lessonModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			if m.tab == tabPractice && m.awaitNext {
				m.awaitNext = false
				return m, nil
			}
			m.closed = true
			return m, nil
		case "tab":
			if m.tab == tabTheory {
				m.tab = tabPractice
			} else {
				m.tab = tabTheory
			}
			return m, m.input.Focus()
		case "ctrl+l":
			m.output = nil
			m.refreshOutputVP()
			return m, nil
		}
		if m.tab == tabTheory {
			return m.updateTheory(key)
		}
		return m.updatePractice(key)
	}
	// keep the text input handling other messages (paste etc.)
	if m.tab == tabPractice {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *lessonModel) updateTheory(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "up", "k":
		m.theoryVP.LineUp(1)
	case "down", "j":
		m.theoryVP.LineDown(1)
	case "pgup", "b":
		m.theoryVP.HalfViewUp()
	case "pgdown", "f", " ":
		m.theoryVP.HalfViewDown()
	case "g":
		m.theoryVP.GotoTop()
	case "G":
		m.theoryVP.GotoBottom()
	}
	return m, nil
}

func (m *lessonModel) updatePractice(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := key.String()
	switch {
	case s == "enter":
		if m.awaitNext {
			return m.advance()
		}
		return m.submit()
	case s == "up":
		return m.historyPrev()
	case s == "down":
		return m.historyNext()
	case s == "pgup":
		m.outVP.HalfViewUp()
	case s == "pgdown":
		m.outVP.HalfViewDown()
	case s == "f1":
		if h, ok := m.session.ShowHint(); ok {
			m.appendOut(styleHint.Render("Подсказка: ") + h)
			m.refreshOutputVP()
		} else {
			m.appendOut(styleDimLine("Подсказок больше нет."))
		}
		return m, nil
	case s == "f2":
		sol := m.session.Exercise().Solution
		if sol != "" {
			m.appendOut(styleHint.Render("Решение: ") + styleCode.Render(sol))
			m.appendOut(styleDimLine("Введите команду самостоятельно и нажмите Enter."))
			m.refreshOutputVP()
		}
		return m, nil
	case s == "f3":
		if err := m.session.Reset(); err != nil {
			m.appendOut(styleDimLine("Не удалось сбросить: " + err.Error()))
		} else {
			m.appendOut(styleDimLine("Песочница упражнения сброшена."))
		}
		m.refreshOutputVP()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(key)
	return m, cmd
}

// submit executes the current input line in the sandbox.
func (m *lessonModel) submit() (tea.Model, tea.Cmd) {
	line := strings.TrimSpace(m.input.Value())
	if line == "" {
		return m, nil
	}
	m.input.SetValue("")
	m.historyBuf = ""
	m.historyIdx = 0

	prompt := m.session.Shell.Prompt()
	m.appendOut(stylePrompt.Render(prompt) + line)

	res := m.session.Execute(line)
	if res.Clear {
		m.output = nil
	}
	if res.Stdout != "" {
		m.appendOut(styleOutput.Render(strings.TrimRight(res.Stdout, "\n")))
	}
	if res.Stderr != "" {
		m.appendOut(styleStderr.Render(strings.TrimRight(res.Stderr, "\n")))
	}

	if m.session.JustSolved() {
		m.prog.MarkExercise(m.lesson.ID, m.session.Exercise().ID)
		if err := m.prog.Save(); err != nil {
			m.appendOut(styleStderr.Render("Не удалось сохранить прогресс: " + err.Error()))
		}
		if m.session.LessonDone() {
			m.prog.MarkLessonFinished(m.lesson, m.session.SolvedCount())
			if err := m.prog.Save(); err != nil {
				m.appendOut(styleStderr.Render("Не удалось сохранить прогресс: " + err.Error()))
			}
			m.appendOut(styleSuccess.Render("✓ Урок полностью пройден! Нажмите Enter."))
		} else {
			m.appendOut(styleSuccess.Render("✓ Задание выполнено! Enter — следующее."))
		}
		m.awaitNext = true
	}
	m.refreshOutputVP()
	return m, nil
}

// advance moves to the next exercise or finishes the lesson.
func (m *lessonModel) advance() (tea.Model, tea.Cmd) {
	m.awaitNext = false
	ok, err := m.session.NextExercise()
	if err != nil {
		// sandbox re-creation failed: stay on the current exercise instead
		// of pretending the lesson is finished
		m.appendOut(styleStderr.Render("Не удалось пересоздать песочницу: " + err.Error()))
		m.refreshOutputVP()
		return m, nil
	}
	if !ok {
		m.finishedLesson = true
		return m, nil
	}
	m.output = nil
	m.appendOut(styleDimLine("Новое задание. Песочница пересоздана."))
	m.refreshOutputVP()
	return m, nil
}

func (m *lessonModel) historyPrev() (tea.Model, tea.Cmd) {
	h := m.session.Shell.History
	if len(h) == 0 {
		return m, nil
	}
	if m.historyIdx == 0 {
		m.historyBuf = m.input.Value()
	}
	if m.historyIdx < len(h) {
		m.historyIdx++
		m.input.SetValue(h[len(h)-m.historyIdx])
		m.input.CursorEnd()
	}
	return m, nil
}

func (m *lessonModel) historyNext() (tea.Model, tea.Cmd) {
	h := m.session.Shell.History
	switch {
	case m.historyIdx == 0:
		return m, nil
	case m.historyIdx == 1:
		m.historyIdx = 0
		m.input.SetValue(m.historyBuf)
	default:
		m.historyIdx--
		m.input.SetValue(h[len(h)-m.historyIdx])
	}
	m.input.CursorEnd()
	return m, nil
}

func (m *lessonModel) appendOut(lines ...string) {
	for _, l := range lines {
		m.output = append(m.output, strings.Split(l, "\n")...)
	}
	if len(m.output) > maxOutputLines {
		m.output = m.output[len(m.output)-maxOutputLines:]
	}
}

func (m *lessonModel) refreshOutputVP() {
	content := strings.Join(m.output, "\n")
	m.outVP.SetContent(content)
	m.outVP.GotoBottom()
}

// View implements tea.Model.
func (m *lessonModel) View() string {
	if m.width == 0 || m.height == 0 {
		return "загрузка..."
	}
	var b strings.Builder
	b.WriteString(m.viewHeader())
	if m.tab == tabTheory {
		b.WriteString("\n")
		b.WriteString(m.theoryVP.View())
		b.WriteString("\n" + styleStatusBar.Render("↑/↓ прокрутка · Tab практика · Esc назад"))
		return b.String()
	}
	b.WriteString("\n")
	b.WriteString(m.viewPractice())
	return b.String()
}

func (m *lessonModel) viewHeader() string {
	solved := m.session.SolvedCount()
	total := len(m.session.Lesson.Exercises)
	progress := styleSubtitle.Render(fmt.Sprintf("%d/%d решено", solved, total))

	var tabs string
	if m.tab == tabTheory {
		tabs = styleTabActive.Render("1 Теория") + styleTab.Render("2 Практика")
	} else {
		tabs = styleTab.Render("1 Теория") + styleTabActive.Render("2 Практика")
	}
	left := styleBold.Render(truncate(m.lesson.Title, m.width-40)) + "  " + progress
	return styleHeader.Render(left) + tabs
}

func (m *lessonModel) viewPractice() string {
	inner := m.width - 6
	if inner < 30 {
		inner = 30
	}

	// task box
	ex := m.session.Exercise()
	var task strings.Builder
	status := "Задание"
	if m.session.IsSolved() {
		status = "Задание ✓"
	}
	task.WriteString(styleBold.Render(fmt.Sprintf("%s %d/%d: ", status, m.session.ExIndex+1, len(m.session.Lesson.Exercises))))
	task.WriteString(strings.Join(wrapText(ex.Task, inner-4), "\n"))
	for i := 0; i < m.session.HintsShown && i < len(ex.Hints); i++ {
		task.WriteString("\n" + styleHint.Render("💡 "+ex.Hints[i]))
	}
	taskBox := styleTaskBox.Render(task.String())

	// terminal box: output viewport + prompt line
	prompt := m.session.Shell.Prompt()
	promptLine := stylePrompt.Render(prompt) + m.input.View()
	if m.awaitNext {
		promptLine = styleSuccess.Render("› Enter — продолжить")
	}
	termHeight := m.height - lipgloss.Height(taskBox) - lipgloss.Height(m.viewHeader()) - 6
	if termHeight < 3 {
		termHeight = 3
	}
	m.outVP.Height = termHeight - 2
	term := styleTermBox.Render(m.outVP.View() + "\n" + promptLine)

	statusBar := styleStatusBar.Render("Enter выполнить · ↑/↓ история · F1 подсказка · F2 решение · F3 сброс · Tab теория · Esc назад")

	return taskBox + "\n" + term + "\n" + truncate(statusBar, m.width)
}

func styleDimLine(s string) string {
	return styleSubtitle.Render(s)
}
