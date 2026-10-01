package vshell

import (
	"fmt"
)

func cronCmds() []Cmd {
	return []Cmd{
		{
			Name: "crontab",
			Man: `crontab — задания планировщика cron
Синтаксис: crontab [-l] [-r] | crontab ФАЙЛ
  -l  показать текущие задания
  -r  удалить все задания
  crontab файл — установить задания из файла
Формат строки: МИН ЧАС ДЕНЬ МЕС ДЕНЬ_НЕД КОМАНДА
Пример: 0 3 * * * /usr/bin/backup.sh — ежедневно в 03:00`,
			Run: func(c *Ctx) int {
				s := c.Shell
				bools, _, operands, err := parseArgs("lre", c.Args[1:], nil)
				if err != nil {
					return errPlain(c, err.Error())
				}
				switch {
				case bools['l']:
					if len(s.Crontab) == 0 {
						fmt.Fprintf(c.Stderr, "no crontab for %s\n", s.User)
						return 1
					}
					for _, l := range s.Crontab {
						fmt.Fprintln(c.Stdout, l)
					}
					return 0
				case bools['r']:
					s.Crontab = nil
					return 0
				case len(operands) > 0:
					content, rerr := s.FS.ReadFile(s.Cwd, operands[0])
					if rerr != nil {
						return errNode(c, operands[0], rerr)
					}
					// real crontab installs the file as-is: comments and
					// blank lines stay, and a comment-only file is valid
					s.Crontab = splitLines(content)
					return 0
				}
				return errPlain(c, "usage: crontab [-l|-r] | crontab FILE")
			},
		},
	}
}
