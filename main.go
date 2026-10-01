// terminalTren — интерактивный тренажёр командной строки Linux.
//
// Обучающие материалы лежат во внешних JSON-файлах каталога lessons/,
// их можно править и добавлять без пересборки программы.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"terminalTren/internal/curriculum"
	"terminalTren/internal/trainer"
	"terminalTren/internal/ui"
)

func main() {
	lessonsDir := flag.String("lessons", "lessons", "каталог с уроками (basic/ и advanced/)")
	flag.Parse()

	cur, err := curriculum.LoadDir(*lessonsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка загрузки уроков из %s: %v\n", *lessonsDir, err)
		fmt.Fprintln(os.Stderr, "Запустите программу из каталога, содержащего папку lessons/,")
		fmt.Fprintln(os.Stderr, "или укажите путь: terminalTren -lessons /путь/к/lessons")
		os.Exit(1)
	}

	prog := trainer.LoadProgress()
	if _, err := tea.NewProgram(ui.NewApp(cur, prog), tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка интерфейса: %v\n", err)
		os.Exit(1)
	}
}
