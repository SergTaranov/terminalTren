package vshell

import (
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"
)

func navCmds() []Cmd {
	return []Cmd{
		{
			Name: "pwd",
			Man: `pwd — вывести имя текущего каталога
Синтаксис: pwd
Печатает абсолютный путь рабочего каталога.`,
			Run: func(c *Ctx) int {
				fmt.Fprintln(c.Stdout, c.Shell.Cwd)
				return 0
			},
		},
		{
			Name: "cd",
			Man: `cd — сменить каталог
Синтаксис: cd [каталог]
Без аргументов переходит в домашний каталог (~).
cd - возвращается в предыдущий каталог.
Пути: /abs/path, relative/path, .. (родитель), . (текущий), ~ (домашний).`,
			Run: func(c *Ctx) int {
				s := c.Shell
				target := ""
				switch {
				case len(c.Args) < 2:
					target = s.FS.Home
				case c.Args[1] == "-":
					if s.prevDir == "" {
						fmt.Fprintln(c.Stderr, "bash: cd: OLDPWD not set")
						return 1
					}
					target = s.prevDir
					fmt.Fprintln(c.Stdout, target)
				default:
					target = c.Args[1]
				}
				n, err := s.FS.Lookup(s.Cwd, target)
				if err != nil {
					fmt.Fprintf(c.Stderr, "bash: cd: %s: %s\n", target, err)
					return 1
				}
				if n.Type != TypeDir {
					fmt.Fprintf(c.Stderr, "bash: cd: %s: %s\n", target, ErrNotDir)
					return 1
				}
				abs := s.FS.AbsPath(s.Cwd, target)
				if !mayEnter(n, s.User) {
					fmt.Fprintf(c.Stderr, "bash: cd: %s: Permission denied\n", target)
					return 1
				}
				s.prevDir = s.Cwd
				s.Cwd = abs
				s.Env["PWD"] = abs
				return 0
			},
		},
		{
			Name: "ls",
			Man: `ls — показать содержимое каталога
Синтаксис: ls [-l] [-a] [-h] [-R] [каталог|файл...]
  -l  подробный список (права, владелец, размер, дата)
  -a  показывать скрытые файлы (начинаются с точки)
  -h  размеры в человекочитаемом виде (с -l)
  -R  рекурсивно по подкаталогам
Без аргументов — текущий каталог.`,
			Run: lsRun,
		},
		{
			Name: "tree",
			Man: `tree — дерево каталогов
Синтаксис: tree [-a] [-d] [каталог]
  -a  показывать скрытые файлы
  -d  только каталоги
Рисует структуру каталогов в виде дерева.`,
			Run: func(c *Ctx) int {
				s := c.Shell
				bools, _, operands, err := parseArgs("ad", c.Args[1:], map[string]byte{"all": 'a'})
				if err != nil {
					return errPlain(c, err.Error())
				}
				root := "."
				if len(operands) > 0 {
					root = operands[0]
				}
				n, lerr := s.FS.Lookup(s.Cwd, root)
				if lerr != nil {
					return errNode(c, root, lerr)
				}
				fmt.Fprintln(c.Stdout, root)
				dirs, files := 0, 0
				var draw func(abs string, node *Node, prefix string)
				draw = func(abs string, node *Node, prefix string) {
					children := node.Children
					names := make([]string, 0, len(children))
					for name := range children {
						if !bools['a'] && strings.HasPrefix(name, ".") {
							continue
						}
						if bools['d'] && children[name].Type != TypeDir {
							continue
						}
						names = append(names, name)
					}
					sort.Strings(names)
					for i, name := range names {
						child := children[name]
						connector, nextPrefix := "├── ", prefix+"│   "
						if i == len(names)-1 {
							connector, nextPrefix = "└── ", prefix+"    "
						}
						fmt.Fprintln(c.Stdout, prefix+connector+name)
						if child.Type == TypeDir {
							dirs++
							draw(path.Join(abs, name), child, nextPrefix)
						} else {
							files++
						}
					}
				}
				if n.Type == TypeDir {
					draw(s.FS.AbsPath(s.Cwd, root), n, "")
				} else {
					// tree of a plain file operand counts the file itself
					files++
				}
				dirWord, fileWord := "directories", "files"
				if dirs == 1 {
					dirWord = "directory"
				}
				if files == 1 {
					fileWord = "file"
				}
				fmt.Fprintf(c.Stdout, "\n%d %s, %d %s\n", dirs, dirWord, files, fileWord)
				return 0
			},
		},
	}
}

func lsRun(c *Ctx) int {
	s := c.Shell
	bools, _, operands, err := parseArgs("laRh", c.Args[1:], map[string]byte{
		"all": 'a', "long": 'l', "recursive": 'R', "human-readable": 'h',
	})
	if err != nil {
		return errPlain(c, err.Error())
	}

	targets := operands
	if len(targets) == 0 {
		targets = []string{"."}
	}

	code := 0
	for i, p := range targets {
		header := len(targets) > 1 || bools['R']
		_ = i
		if c2 := lsListOne(c, s, bools, p, header); c2 != 0 {
			code = c2
		}
	}
	return code
}

// lsListOne prints one directory (or file) for ls.
func lsListOne(c *Ctx, s *Shell, bools map[byte]bool, p string, printHeader bool) int {
	n, lerr := s.FS.Lookup(s.Cwd, p)
	if lerr != nil {
		return errNode(c, p, lerr)
	}
	if n.Type != TypeDir {
		// a file operand still gets a long-format line under -l
		if bools['l'] {
			size := fmt.Sprintf("%d", n.Size())
			if bools['h'] {
				size = humanSize(n.Size())
			}
			suffix := ""
			if n.Type == TypeSymlink {
				suffix = " -> " + n.Target
			}
			fmt.Fprintf(c.Stdout, "%s %d %-8s %-8s %6s %s %s%s\n",
				permString(n), linkCount(n), n.Owner, n.Group, size, lsTimeStamp(n.MTime), p, suffix)
			return 0
		}
		fmt.Fprintln(c.Stdout, p)
		return 0
	}
	if !mayEnter(n, s.User) {
		fmt.Fprintf(c.Stderr, "ls: cannot open directory '%s': Permission denied\n", p)
		return 2
	}
	children := n.Children
	names := make([]string, 0, len(children))
	for name := range children {
		if !bools['a'] && strings.HasPrefix(name, ".") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if bools['a'] {
		names = append([]string{".", ".."}, names...)
	}
	if printHeader {
		fmt.Fprintf(c.Stdout, "%s:\n", p)
	}
	if bools['l'] {
		total := 0
		for _, name := range names {
			if ch, ok := children[name]; ok {
				total += (ch.Size() + 511) / 512 * 512
			}
		}
		// blocks in 1K units, rounded up, minimum 1 like GNU ls
		blocks := (total + 1023) / 1024
		if blocks == 0 && len(names) > 0 {
			blocks = 1
		}
		fmt.Fprintf(c.Stdout, "total %d\n", blocks)
		for _, name := range names {
			var ch *Node
			if nn, ok := children[name]; ok {
				ch = nn
			} else { // . or ..
				ch = &Node{Name: name, Type: TypeDir, Perms: 0o755, Owner: s.User, Group: s.User, MTime: s.FS.Now()}
			}
			size := fmt.Sprintf("%d", ch.Size())
			if bools['h'] {
				size = humanSize(ch.Size())
			}
			suffix := ""
			if ch.Type == TypeSymlink {
				suffix = " -> " + ch.Target
			}
			fmt.Fprintf(c.Stdout, "%s %d %-8s %-8s %6s %s %s%s\n",
				permString(ch), linkCount(ch), ch.Owner, ch.Group, size, lsTimeStamp(ch.MTime), name, suffix)
		}
	} else {
		printColumns(c.Stdout, names)
	}
	if bools['R'] {
		for _, name := range names {
			ch, ok := children[name]
			if !ok || ch.Type != TypeDir {
				continue
			}
			fmt.Fprintln(c.Stdout)
			sub := path.Join(p, name)
			if code := lsListOne(c, s, bools, sub, true); code != 0 {
				return code
			}
		}
	}
	return 0
}

func linkCount(n *Node) int {
	if n.Type == TypeDir {
		return 2 + countSubdirs(n)
	}
	return 1
}

func countSubdirs(n *Node) int {
	count := 0
	for _, ch := range n.Children {
		if ch.Type == TypeDir {
			count++
		}
	}
	return count
}

// lsTimeStamp renders mtime like ls: "Sep 30 20:15" or "Jan  2  2024".
func lsTimeStamp(t time.Time) string {
	if t.Year() == time.Now().Year() {
		return t.Format("Jan _2 15:04")
	}
	return t.Format("Jan _2  2006")
}

// printColumns lays names out in column-major order like ls does.
func printColumns(w io.Writer, names []string) {
	if len(names) == 0 {
		return
	}
	width := 0
	for _, n := range names {
		if len(n) > width {
			width = len(n)
		}
	}
	width++
	const termWidth = 80
	cols := termWidth / width
	if cols < 1 {
		cols = 1
	}
	rows := (len(names) + cols - 1) / cols
	for r := 0; r < rows; r++ {
		var line strings.Builder
		for col := 0; col < cols; col++ {
			idx := col*rows + r
			if idx >= len(names) {
				continue
			}
			line.WriteString(names[idx])
			if col < cols-1 {
				line.WriteString(strings.Repeat(" ", width-len(names[idx])))
			}
		}
		fmt.Fprintln(w, strings.TrimRight(line.String(), " "))
	}
}
