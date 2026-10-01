package vshell

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// externalPrograms are "installed" simulated programs that are not
// implemented as shell built-ins but exist for which/scripts.
var externalPrograms = map[string]bool{
	"python3": true, "python": true, "git": true, "make": true, "gcc": true,
	"nano": true, "vim": true, "htop": true, "wget": true, "ssh": true,
	"scp": true, "node": true, "npm": true, "docker": true, "mysql": true,
}

func sysCmds() []Cmd {
	return []Cmd{
		{
			Name: "echo",
			Man: `echo — вывести текст
Синтаксис: echo [-n] [-e] [текст...]
  -n  без перевода строки в конце
  -e  обрабатывать \n, \t, \\
Переменные раскрываются: echo "Дом: $HOME"`,
			Run: func(c *Ctx) int {
				noNL, esc := false, false
				args := c.Args[1:]
				for len(args) > 0 && strings.HasPrefix(args[0], "-") && len(args[0]) > 1 &&
					args[0] != "--" && strings.Trim(args[0], "-neE") == "" {
					for _, ch := range args[0][1:] {
						if ch == 'n' {
							noNL = true
						}
						if ch == 'e' {
							esc = true
						}
					}
					args = args[1:]
				}
				text := strings.Join(args, " ")
				if esc {
					text = unescape(text)
				}
				if noNL {
					fmt.Fprint(c.Stdout, text)
				} else {
					fmt.Fprintln(c.Stdout, text)
				}
				return 0
			},
		},
		{
			Name: "printf",
			Man: `printf — форматированный вывод
Синтаксис: printf ФОРМАТ [аргументы...]
Форматы: %s строка, %d целое, %f число, %% процент.
printf '%s=%d\n' "ответ" 42`,
			Run: func(c *Ctx) int {
				if len(c.Args) < 2 {
					return errPlain(c, "usage: printf format [arguments...]")
				}
				fmt.Fprint(c.Stdout, printfFormat(unescape(c.Args[1]), c.Args[2:]))
				return 0
			},
		},
		{
			Name: "env",
			Man: `env — показать переменные окружения
Синтаксис: env [ПЕРЕМ=значение] [команда]
Без команды печатает все переменные.
env | grep HOME — найти переменную.`,
			Run: func(c *Ctx) int {
				s := c.Shell
				args := c.Args[1:]
				assigns := map[string]string{}
				for len(args) > 0 && isAssign(args[0]) {
					kv := strings.SplitN(args[0], "=", 2)
					assigns[kv[0]] = kv[1]
					args = args[1:]
				}
				if len(args) == 0 {
					merged := make(map[string]string, len(s.Env)+len(assigns))
					for k, v := range s.Env {
						merged[k] = v
					}
					for k, v := range assigns {
						merged[k] = v
					}
					keys := make([]string, 0, len(merged))
					for k := range merged {
						keys = append(keys, k)
					}
					sortStrings(keys)
					for _, k := range keys {
						fmt.Fprintf(c.Stdout, "%s=%s\n", k, merged[k])
					}
					return 0
				}
				// run the command with the assignments applied
				saved := make(map[string]string, len(assigns))
				existed := map[string]bool{}
				for k, v := range assigns {
					saved[k], existed[k] = s.Env[k], true
					if _, ok := s.Env[k]; !ok {
						existed[k] = false
					}
					s.Env[k] = v
				}
				code := s.runCaptured(args, c)
				for k := range assigns {
					if existed[k] {
						s.Env[k] = saved[k]
					} else {
						delete(s.Env, k)
					}
				}
				return code
			},
		},
		{
			Name: "export",
			Man: `export — сделать переменную окружением
Синтаксис: export ПЕРЕМ=значение
export без аргументов показывает переменные.
Переменная станет видна дочерним процессам: echo $ПЕРЕМ.`,
			Run: func(c *Ctx) int {
				if len(c.Args) == 1 {
					keys := make([]string, 0, len(c.Shell.Env))
					for k := range c.Shell.Env {
						keys = append(keys, k)
					}
					sortStrings(keys)
					for _, k := range keys {
						fmt.Fprintf(c.Stdout, "declare -x %s=\"%s\"\n", k, c.Shell.Env[k])
					}
					return 0
				}
				for _, a := range c.Args[1:] {
					if eq := strings.IndexByte(a, '='); eq > 0 {
						c.Shell.Env[a[:eq]] = a[eq+1:]
					}
					// `export VAR` without a value: bash only marks an
					// existing variable for export and creates nothing
				}
				return 0
			},
		},
		{
			Name: "unset",
			Man: `unset — удалить переменную
Синтаксис: unset ПЕРЕМЕННАЯ...`,
			Run: func(c *Ctx) int {
				for _, a := range c.Args[1:] {
					delete(c.Shell.Env, a)
				}
				return 0
			},
		},
		{
			Name: "whoami",
			Man:  `whoami — имя текущего пользователя`,
			Run: func(c *Ctx) int {
				fmt.Fprintln(c.Stdout, c.Shell.User)
				return 0
			},
		},
		{
			Name: "id",
			Man: `id — информация о пользователе
Синтаксис: id [пользователь]
Показывает uid, gid и группы.`,
			Run: func(c *Ctx) int {
				user := c.Shell.User
				if len(c.Args) > 1 {
					user = c.Args[1]
				}
				switch user {
				case "root":
					fmt.Fprintln(c.Stdout, "uid=0(root) gid=0(root) groups=0(root)")
				case "user":
					fmt.Fprintln(c.Stdout, "uid=1000(user) gid=1000(user) groups=1000(user),27(sudo)")
				default:
					fmt.Fprintf(c.Stderr, "id: '%s': no such user\n", user)
					return 1
				}
				return 0
			},
		},
		{
			Name: "uname",
			Man: `uname — информация о системе
Синтаксис: uname [-r] [-a]
  -r  версия ядра
  -a  всё`,
			Run: func(c *Ctx) int {
				bools, _, _, err := parseArgs("rasnmo", c.Args[1:], map[string]byte{
					"all": 'a', "kernel-release": 'r',
				})
				if err != nil {
					return errPlain(c, err.Error())
				}
				switch {
				case bools['a']:
					fmt.Fprintln(c.Stdout, "Linux trenbox 5.15.0-generic #56-Ubuntu SMP x86_64 GNU/Linux")
				case bools['r']:
					fmt.Fprintln(c.Stdout, "5.15.0-generic")
				case bools['n']:
					fmt.Fprintln(c.Stdout, c.Shell.Env["HOSTNAME"])
				case bools['m']:
					fmt.Fprintln(c.Stdout, "x86_64")
				case bools['o']:
					fmt.Fprintln(c.Stdout, "GNU/Linux")
				default:
					fmt.Fprintln(c.Stdout, "Linux")
				}
				return 0
			},
		},
		{
			Name: "hostname",
			Man:  `hostname — имя хоста`,
			Run: func(c *Ctx) int {
				fmt.Fprintln(c.Stdout, c.Shell.Env["HOSTNAME"])
				return 0
			},
		},
		{
			Name: "date",
			Man: `date — дата и время
Синтаксис: date [-u] [+ФОРМАТ]
  -u  время UTC
Формат: +%Y-%m-%d, +%H:%M, +%s (epoch)
Пример: date +%F`,
			Run: func(c *Ctx) int {
				bools, _, operands, err := parseArgs("u", c.Args[1:], map[string]byte{"utc": 'u'})
				if err != nil {
					return errPlain(c, err.Error())
				}
				now := time.Now()
				if bools['u'] {
					now = now.UTC()
				}
				if len(operands) > 0 && strings.HasPrefix(operands[0], "+") {
					fmt.Fprintln(c.Stdout, formatDate(now, operands[0][1:]))
					return 0
				}
				// MST renders the real zone abbreviation of the time value
				fmt.Fprintln(c.Stdout, now.Format("Mon Jan _2 15:04:05 MST 2006"))
				return 0
			},
		},
		{
			Name: "history",
			Man: `history — история команд
Синтаксис: history
Показывает введённые команды с номерами.`,
			Run: func(c *Ctx) int {
				for i, h := range c.Shell.History {
					fmt.Fprintf(c.Stdout, "%5d  %s\n", i+1, h)
				}
				return 0
			},
		},
		{
			Name: "alias",
			Man: `alias — короткие имена команд
Синтаксис: alias [имя='команда']
alias без аргументов — список алиасов.
Пример: alias ll='ls -la'`,
			Run: func(c *Ctx) int {
				if len(c.Args) == 1 {
					keys := make([]string, 0, len(c.Shell.Aliases))
					for k := range c.Shell.Aliases {
						keys = append(keys, k)
					}
					sortStrings(keys)
					for _, k := range keys {
						fmt.Fprintf(c.Stdout, "alias %s='%s'\n", k, c.Shell.Aliases[k])
					}
					return 0
				}
				code := 0
				for _, a := range c.Args[1:] {
					if eq := strings.IndexByte(a, '='); eq > 0 {
						c.Shell.Aliases[a[:eq]] = strings.Trim(a[eq+1:], "'\"")
					} else if v, ok := c.Shell.Aliases[a]; ok {
						fmt.Fprintf(c.Stdout, "alias %s='%s'\n", a, v)
					} else {
						fmt.Fprintf(c.Stderr, "bash: alias: %s: not found\n", a)
						code = 1
					}
				}
				return code
			},
		},
		{
			Name: "unalias",
			Man: `unalias — удалить алиас
Синтаксис: unalias имя`,
			Run: func(c *Ctx) int {
				for _, a := range c.Args[1:] {
					delete(c.Shell.Aliases, a)
				}
				return 0
			},
		},
		{
			Name: "man",
			Man: `man — справка по команде
Синтаксис: man команда
Показывает краткое русское описание.
help — список всех команд тренажёра.`,
			Run: func(c *Ctx) int {
				if len(c.Args) < 2 {
					fmt.Fprintln(c.Stdout, "What manual page do you want?\nTry: man ls")
					return 1
				}
				cmd := c.Shell.LookupCommand(c.Args[1])
				if cmd == nil {
					fmt.Fprintf(c.Stderr, "No manual entry for %s\n", c.Args[1])
					return 1
				}
				fmt.Fprintln(c.Stdout, cmd.Man)
				return 0
			},
		},
		{
			Name: "help",
			Man: `help — список команд тренажёра
Синтаксис: help
man команда — подробная справка.`,
			Run: func(c *Ctx) int {
				names := c.Shell.CommandNames()
				fmt.Fprintln(c.Stdout, "Доступные команды тренажёра:")
				printColumns(c.Stdout, names)
				fmt.Fprintln(c.Stdout, "\nman <команда> — справка по команде")
				return 0
			},
		},
		{
			Name: "clear",
			Man:  `clear — очистить экран терминала`,
			Run: func(c *Ctx) int {
				c.clearFlag = true
				return 0
			},
		},
		{
			Name: "seq",
			Man: `seq — последовательность чисел
Синтаксис: seq КОНЕЦ | seq НАЧАЛО КОНЕЦ | seq НАЧАЛО ШАГ КОНЕЦ
  -w  выравнивать числа по ширине нулями
seq 1 5 | wc -l — посчитать`,
			Run: func(c *Ctx) int {
				bools, _, operands, err := parseArgs("w", c.Args[1:], map[string]byte{"equal-width": 'w'})
				if err != nil {
					return errPlain(c, err.Error())
				}
				nums := make([]float64, 0, 3)
				for _, a := range operands {
					f, perr := strconv.ParseFloat(a, 64)
					if perr != nil {
						return errPlain(c, "invalid floating point argument: '"+a+"'")
					}
					nums = append(nums, f)
				}
				var start, step, end float64
				switch len(nums) {
				case 1:
					start, step, end = 1, 1, nums[0]
				case 2:
					start, step, end = nums[0], 1, nums[1]
				case 3:
					start, step, end = nums[0], nums[1], nums[2]
				default:
					return errPlain(c, "missing operand")
				}
				if step == 0 {
					return errPlain(c, "invalid Zero increment value")
				}
				width := 0
				if bools['w'] {
					width = len(fmt.Sprintf("%d", int64(end)))
					if w := len(fmt.Sprintf("%d", int64(start))); w > width {
						width = w
					}
				}
				for v := start; (step > 0 && v <= end) || (step < 0 && v >= end); v += step {
					if v == float64(int64(v)) {
						if width > 0 {
							fmt.Fprintf(c.Stdout, "%0*d\n", width, int64(v))
						} else {
							fmt.Fprintln(c.Stdout, int64(v))
						}
					} else {
						fmt.Fprintf(c.Stdout, "%g\n", v)
					}
				}
				return 0
			},
		},
		{
			Name: "true",
			Man:  `true — всегда код возврата 0`,
			Run:  func(c *Ctx) int { return 0 },
		},
		{
			Name: "false",
			Man:  `false — всегда код возврата 1`,
			Run:  func(c *Ctx) int { return 1 },
		},
		{
			Name: "test",
			Man: `test — проверка условия
Синтаксис: test УСЛОВИЕ  или  [ УСЛОВИЕ ]
Файлы: -e есть, -f файл, -d каталог, -r читаем, -w записываем, -x исполняем, -s не пуст
Строки: -z пустая, -n непустая, A = B, A != B
Числа: -eq -ne -lt -le -gt -ge
Код возврата: 0 — истина, 1 — ложь.`,
			Run: testRun,
		},
		{
			Name: "[",
			Man: `[ — синоним test
Синтаксис: [ УСЛОВИЕ ]
Обратите внимание на пробелы и закрывающую ].`,
			Run: func(c *Ctx) int {
				if len(c.Args) < 2 || c.Args[len(c.Args)-1] != "]" {
					fmt.Fprintln(c.Stderr, "bash: [: missing `]'")
					return 2
				}
				sub := &Ctx{Shell: c.Shell, Args: append([]string{"test"}, c.Args[1:len(c.Args)-1]...),
					Stdin: c.Stdin, Stdout: c.Stdout, Stderr: c.Stderr}
				return testRun(sub)
			},
		},
		{
			Name: "which",
			Man: `which — путь к команде
Синтаксис: which команда...`,
			Run: func(c *Ctx) int {
				code := 0
				for _, name := range c.Args[1:] {
					if c.Shell.LookupCommand(name) != nil || externalPrograms[name] {
						fmt.Fprintf(c.Stdout, "/usr/bin/%s\n", name)
						continue
					}
					if strings.Contains(name, "/") {
						if n, err := c.Shell.FS.Lookup(c.Shell.Cwd, name); err == nil && CanExec(n, c.Shell.User) {
							fmt.Fprintln(c.Stdout, name)
							continue
						}
					}
					code = 1
				}
				return code
			},
		},
		{
			Name: "df",
			Man: `df — место на дисках
Синтаксис: df [-h]
  -h  человекочитаемые размеры (Г, М)`,
			Run: func(c *Ctx) int {
				bools, _, _, err := parseArgs("h", c.Args[1:], map[string]byte{"human-readable": 'h'})
				if err != nil {
					return errPlain(c, err.Error())
				}
				rows := []struct{ fs, size, used, avail, pct, mount string }{
					{"/dev/sda1", "20G", "5.2G", "14G", "28%", "/"},
					{"tmpfs", "1.9G", "0", "1.9G", "0%", "/dev/shm"},
					{"/dev/sda2", "100G", "42G", "53G", "45%", "/home"},
				}
				if bools['h'] {
					fmt.Fprintln(c.Stdout, "Filesystem      Size  Used Avail Use% Mounted on")
					for _, r := range rows {
						fmt.Fprintf(c.Stdout, "%-14s %5s %5s %5s %4s %s\n", r.fs, r.size, r.used, r.avail, r.pct, r.mount)
					}
				} else {
					fmt.Fprintln(c.Stdout, "Filesystem     1K-blocks     Used Available Use% Mounted on")
					for _, r := range rows {
						fmt.Fprintf(c.Stdout, "%-14s %10s %8s %9s %4s %s\n", r.fs,
							blocksOf(r.size), blocksOf(r.used), blocksOf(r.avail), r.pct, r.mount)
					}
				}
				return 0
			},
		},
		{
			Name: "du",
			Man: `du — размер каталогов и файлов
Синтаксис: du [-h] [-s] [путь...]
  -s  только итоговый размер
  -h  человекочитаемо
du -sh каталог — сколько занимает каталог.`,
			Run: func(c *Ctx) int {
				s := c.Shell
				bools, _, operands, err := parseArgs("hs", c.Args[1:], map[string]byte{
					"human-readable": 'h', "summarize": 's',
				})
				if err != nil {
					return errPlain(c, err.Error())
				}
				if len(operands) == 0 {
					operands = []string{"."}
				}
				code := 0
				for _, p := range operands {
					n, lerr := s.FS.Lookup(s.Cwd, p)
					if lerr != nil {
						code = errNode(c, p, lerr)
						continue
					}
					abs := s.FS.AbsPath(s.Cwd, p)
					display := displayPath(s, p, abs)
					printDu := func(size int, name string) {
						if bools['h'] {
							fmt.Fprintf(c.Stdout, "%s\t%s\n", humanSize(size), name)
						} else {
							fmt.Fprintf(c.Stdout, "%d\t%s\n", blocks4K(size), name)
						}
					}
					if n.Type != TypeDir {
						// du of a plain file reports the file itself
						printDu(n.Size(), display)
						continue
					}
					if bools['s'] {
						printDu(s.FS.DirSize(abs, n), display)
						continue
					}
					_ = s.FS.Walk(s.Cwd, p, func(wabs string, wn *Node) error {
						if wn.Type != TypeDir {
							return nil
						}
						wd := displayPath(s, p, wabs)
						if wabs == abs {
							wd = display
						}
						printDu(s.FS.DirSize(wabs, wn), wd)
						return nil
					})
				}
				return code
			},
		},
		{
			Name: "free",
			Man: `free — память и своп
Синтаксис: free [-h]
  -h  в гигабайтах`,
			Run: func(c *Ctx) int {
				bools, _, _, err := parseArgs("hm", c.Args[1:], map[string]byte{"human": 'h'})
				if err != nil {
					return errPlain(c, err.Error())
				}
				if bools['h'] {
					fmt.Fprint(c.Stdout, `               total        used        free      shared  buff/cache   available
Mem:           3.9Gi       1.2Gi       2.0Gi        12Mi       567Mi       2.6Gi
Swap:          2.0Gi          0B       2.0Gi
`)
				} else {
					fmt.Fprint(c.Stdout, `               total        used        free      shared  buff/cache   available
Mem:         3901332     1234567     2100001       12288      580244     2666765
Swap:        2097148           0     2097148
`)
				}
				return 0
			},
		},
		{
			Name: "lsblk",
			Man: `lsblk — список блочных устройств
Синтаксис: lsblk
Показывает диски и разделы.`,
			Run: func(c *Ctx) int {
				fmt.Fprint(c.Stdout, `NAME   MAJ:MIN RM   SIZE RO TYPE MOUNTPOINTS
sda      8:0    0   120G  0 disk 
├─sda1   8:1    0    20G  0 part /
└─sda2   8:2    0   100G  0 part /home
sr0     11:0    1  1024M  0 rom  
`)
				return 0
			},
		},
		{
			Name: "sleep",
			Man: `sleep — пауза
Синтаксис: sleep СЕКУНДЫ
В тренажёре срабатывает мгновенно.`,
			Run: func(c *Ctx) int { return 0 },
		},
		{
			Name: "exit",
			Man: `exit — выйти из shell/скрипта
Синтаксис: exit [код]
В тренажёре для выхода нажмите Esc.`,
			Run: func(c *Ctx) int { return 0 },
		},
	}
}

// testRun implements test/[ conditions.
func testRun(c *Ctx) int {
	args := c.Args[1:]
	s := c.Shell

	negate := false
	if len(args) > 0 && args[0] == "!" {
		negate = true
		args = args[1:]
	}

	result := false
	switch {
	case len(args) == 0:
		result = false
	case len(args) == 3 && isBinaryOp(args[1]):
		result = evalBinary(c, args[0], args[1], args[2])
	case len(args) == 2 && strings.HasPrefix(args[0], "-"):
		result = evalUnary(s, args[0], args[1])
	case len(args) == 1:
		result = args[0] != ""
	default:
		fmt.Fprintln(c.Stderr, "bash: test: too many arguments")
		return 2
	}
	if negate {
		result = !result
	}
	if result {
		return 0
	}
	return 1
}

func isBinaryOp(op string) bool {
	switch op {
	case "=", "==", "!=", "-eq", "-ne", "-lt", "-le", "-gt", "-ge":
		return true
	}
	return false
}

func evalBinary(c *Ctx, a, op, b string) bool {
	switch op {
	case "=", "==":
		return a == b
	case "!=":
		return a != b
	}
	na, erra := strconv.Atoi(a)
	nb, errb := strconv.Atoi(b)
	if erra != nil || errb != nil {
		fmt.Fprintf(c.Stderr, "test: %s: integer expression expected\n", op)
		return false
	}
	switch op {
	case "-eq":
		return na == nb
	case "-ne":
		return na != nb
	case "-lt":
		return na < nb
	case "-le":
		return na <= nb
	case "-gt":
		return na > nb
	case "-ge":
		return na >= nb
	}
	return false
}

func evalUnary(s *Shell, op, arg string) bool {
	switch op {
	case "-z":
		return arg == ""
	case "-n":
		return arg != ""
	}
	n, err := s.FS.Lookup(s.Cwd, arg)
	if err != nil {
		return false
	}
	switch op {
	case "-e":
		return true
	case "-f":
		return n.Type == TypeFile
	case "-d":
		return n.Type == TypeDir
	case "-L", "-h":
		return n.Type == TypeSymlink
	case "-r":
		return CanRead(n, s.User)
	case "-w":
		return CanWrite(n, s.User)
	case "-x":
		return CanExec(n, s.User)
	case "-s":
		return n.Size() > 0
	}
	return false
}

// formatDate converts common date(1) format specifiers.
func formatDate(t time.Time, format string) string {
	isoWeekday := int(t.Weekday())
	if isoWeekday == 0 {
		isoWeekday = 7
	}
	replacements := []string{
		"%Y", "2006", "%m", "01", "%d", "02",
		"%H", "15", "%M", "04", "%S", "05",
		"%y", "06", "%F", "2006-01-02", "%T", "15:04:05",
		"%s", fmt.Sprintf("%d", t.Unix()),
		"%A", "Monday", "%a", "Mon", "%B", "January", "%b", "Jan",
		"%p", t.Format("PM"), "%u", strconv.Itoa(isoWeekday),
	}
	out := format
	for i := 0; i+1 < len(replacements); i += 2 {
		out = strings.ReplaceAll(out, replacements[i], replacements[i+1])
	}
	return t.Format(out)
}

func unescape(s string) string {
	r := strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\\`, `\`, `\r`, "\r")
	return r.Replace(s)
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// blocksOf converts "20G"-style sizes to 1K blocks for df.
func blocksOf(h string) string {
	f := 0.0
	unit := 1.0
	switch {
	case strings.HasSuffix(h, "G"):
		f = parseFloat(strings.TrimSuffix(h, "G"))
		unit = 1024 * 1024
	case strings.HasSuffix(h, "M"):
		f = parseFloat(strings.TrimSuffix(h, "M"))
		unit = 1024
	case strings.HasSuffix(h, "K"):
		f = parseFloat(strings.TrimSuffix(h, "K"))
	default:
		f = parseFloat(h)
	}
	return strconv.Itoa(int(f * unit))
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// blocks4K converts bytes to 4K-block count like du's default units.
func blocks4K(n int) int {
	return (n + 4095) / 4096 * 4
}
