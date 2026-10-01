package vshell

import (
	"fmt"
	"path"
)

func fileCmds() []Cmd {
	return []Cmd{
		{
			Name: "touch",
			Man: `touch — создать пустой файл или обновить время изменения
Синтаксис: touch файл...
Если файл существует — обновляет его mtime, иначе создаёт пустой.`,
			Run: func(c *Ctx) int {
				s := c.Shell
				_, _, operands, err := parseArgs("", c.Args[1:], nil)
				if err != nil {
					return errPlain(c, err.Error())
				}
				if len(operands) == 0 {
					return errPlain(c, "missing file operand")
				}
				code := 0
				for _, p := range operands {
					if n, lerr := s.FS.LookupNoFollow(s.Cwd, p); lerr == nil {
						if !CanWrite(n, s.User) {
							fmt.Fprintf(c.Stderr, "touch: cannot touch '%s': Permission denied\n", p)
							code = 1
							continue
						}
						n.MTime = s.FS.Now()
						continue
					}
					parent, perr := s.FS.Lookup("/", path.Dir(s.FS.AbsPath(s.Cwd, p)))
					if perr == nil && !CanWrite(parent, s.User) {
						fmt.Fprintf(c.Stderr, "touch: cannot touch '%s': Permission denied\n", p)
						code = 1
						continue
					}
					if _, cerr := s.FS.Create(s.Cwd, p, 0o644&^s.Umask, s.User, s.User); cerr != nil {
						code = errNode(c, p, cerr)
					}
				}
				return code
			},
		},
		{
			Name: "mkdir",
			Man: `mkdir — создать каталог
Синтаксис: mkdir [-p] каталог...
  -p  создать все родительские каталоги и не ошибаться, если существует`,
			Run: func(c *Ctx) int {
				s := c.Shell
				bools, _, operands, err := parseArgs("p", c.Args[1:], map[string]byte{"parents": 'p'})
				if err != nil {
					return errPlain(c, err.Error())
				}
				if len(operands) == 0 {
					return errPlain(c, "missing operand")
				}
				code := 0
				for _, p := range operands {
					mode := 0o755 &^ s.Umask
					if bools['p'] {
						if err := s.FS.MkdirAll(s.Cwd, p, mode, s.User, s.User); err != nil && err != ErrExist {
							code = errNode(c, p, err)
						}
						continue
					}
					if err := s.FS.Mkdir(s.Cwd, p, mode, s.User, s.User); err != nil {
						code = errNode(c, p, err)
					}
				}
				return code
			},
		},
		{
			Name: "rmdir",
			Man: `rmdir — удалить пустой каталог
Синтаксис: rmdir каталог...
Ошибка, если каталог не пуст (используйте rm -r).`,
			Run: func(c *Ctx) int {
				s := c.Shell
				_, _, operands, err := parseArgs("", c.Args[1:], nil)
				if err != nil {
					return errPlain(c, err.Error())
				}
				if len(operands) == 0 {
					return errPlain(c, "missing operand")
				}
				code := 0
				for _, p := range operands {
					if err := s.FS.Remove(s.Cwd, p, false); err != nil {
						code = errNode(c, p, err)
					}
				}
				return code
			},
		},
		{
			Name: "rm",
			Man: `rm — удалить файл или каталог
Синтаксис: rm [-r] [-f] [-i] [-d] файл...
  -r  рекурсивно (для каталогов, обязательно!)
  -f  не спрашивать подтверждения и игнорировать несуществующие
  -i  спрашивать подтверждение (упрощённо: не спрашивает)
  -d  удалить пустой каталог
Без -r/-d каталоги не удаляются.`,
			Run: func(c *Ctx) int {
				s := c.Shell
				bools, _, operands, err := parseArgs("rfid", c.Args[1:], map[string]byte{
					"recursive": 'r', "force": 'f', "interactive": 'i', "dir": 'd',
				})
				if err != nil {
					return errPlain(c, err.Error())
				}
				if len(operands) == 0 {
					return errPlain(c, "missing operand")
				}
				code := 0
				for _, p := range operands {
					n, lerr := s.FS.LookupNoFollow(s.Cwd, p)
					if lerr != nil {
						if bools['f'] {
							continue
						}
						code = errNode(c, p, lerr)
						continue
					}
					if n.Type == TypeDir && !bools['r'] && !bools['d'] {
						// real rm refuses any directory without -r/-d
						code = errNode(c, p, ErrIsDir)
						continue
					}
					parentDir := s.FS.AbsPath(s.Cwd, path.Dir(p))
					parent, _ := s.FS.Lookup(s.Cwd, parentDir)
					if parent != nil && !CanWrite(parent, s.User) {
						fmt.Fprintf(c.Stderr, "rm: cannot remove '%s': Permission denied\n", p)
						code = 1
						continue
					}
					if err := s.FS.Remove(s.Cwd, p, bools['r']); err != nil {
						if bools['f'] {
							continue
						}
						code = errNode(c, p, err)
					}
				}
				return code
			},
		},
		{
			Name: "cp",
			Man: `cp — копировать файлы и каталоги
Синтаксис: cp [-r] источник... назначение
  -r  рекурсивно (для каталогов)
Если назначение — каталог, копирует внутрь него.`,
			Run: func(c *Ctx) int {
				s := c.Shell
				bools, _, operands, err := parseArgs("r", c.Args[1:], map[string]byte{
					"recursive": 'r', "copy-contents": 'r',
				})
				if err != nil {
					return errPlain(c, err.Error())
				}
				if len(operands) < 2 {
					return errPlain(c, "missing destination file operand")
				}
				dst := operands[len(operands)-1]
				code := 0
				for _, src := range operands[:len(operands)-1] {
					sn, lerr := s.FS.LookupNoFollow(s.Cwd, src)
					if lerr != nil {
						code = errNode(c, src, lerr)
						continue
					}
					if !CanRead(sn, s.User) {
						fmt.Fprintf(c.Stderr, "cp: cannot open '%s' for reading: Permission denied\n", src)
						code = 1
						continue
					}
					if !s.canCreateAt(dst, path.Base(src)) {
						fmt.Fprintf(c.Stderr, "cp: cannot create regular file '%s': Permission denied\n",
							s.targetPathFor(dst, path.Base(src)))
						code = 1
						continue
					}
					if err := s.FS.Copy(s.Cwd, src, dst, bools['r'], s.User, s.User, s.Umask); err != nil {
						code = errNode(c, src, err)
					}
				}
				return code
			},
		},
		{
			Name: "mv",
			Man: `mv — переместить/переименовать
Синтаксис: mv источник... назначение
Если назначение — каталог, перемещает внутрь него.
Переименование = перемещение в тот же каталог.`,
			Run: func(c *Ctx) int {
				s := c.Shell
				_, _, operands, err := parseArgs("", c.Args[1:], nil)
				if err != nil {
					return errPlain(c, err.Error())
				}
				if len(operands) < 2 {
					return errPlain(c, "missing destination file operand")
				}
				dst := operands[len(operands)-1]
				code := 0
				for _, src := range operands[:len(operands)-1] {
					if !s.canRemoveFrom(src) || !s.canCreateAt(dst, path.Base(src)) {
						fmt.Fprintf(c.Stderr, "mv: cannot move '%s' to '%s': Permission denied\n", src, dst)
						code = 1
						continue
					}
					if err := s.FS.Move(s.Cwd, src, dst); err != nil {
						code = errNode(c, src, err)
					}
				}
				return code
			},
		},
		{
			Name: "ln",
			Man: `ln — создать ссылку
Синтаксис: ln [-s] цель [имя_ссылки]
  -s  символическая (мягкая) ссылка
Без -s создаёт жёсткую ссылку (в тренажёре упрощена до -s).`,
			Run: func(c *Ctx) int {
				s := c.Shell
				_, _, operands, err := parseArgs("s", c.Args[1:], map[string]byte{"symbolic": 's'})
				if err != nil {
					return errPlain(c, err.Error())
				}
				if len(operands) == 0 {
					return errPlain(c, "missing file operand")
				}
				target := operands[0]
				name := ""
				if len(operands) > 1 {
					name = operands[1]
				} else {
					name = path.Base(target)
				}
				_ = s
				if err := s.FS.Symlink(s.Cwd, name, target, s.User, s.User); err != nil {
					return errNode(c, name, err)
				}
				return 0
			},
		},
		{
			Name: "basename",
			Man: `basename — имя файла без каталога
Синтаксис: basename путь [суффикс]`,
			Run: func(c *Ctx) int {
				_, _, operands, err := parseArgs("", c.Args[1:], nil)
				if err != nil || len(operands) == 0 {
					return errPlain(c, "missing operand")
				}
				name := path.Base(operands[0])
				if len(operands) > 1 {
					name = trimSuffix(name, operands[1])
				}
				fmt.Fprintln(c.Stdout, name)
				return 0
			},
		},
		{
			Name: "dirname",
			Man: `dirname — каталог пути
Синтаксис: dirname путь...`,
			Run: func(c *Ctx) int {
				_, _, operands, err := parseArgs("", c.Args[1:], nil)
				if err != nil || len(operands) == 0 {
					return errPlain(c, "missing operand")
				}
				for _, p := range operands {
					fmt.Fprintln(c.Stdout, path.Dir(p))
				}
				return 0
			},
		},
	}
}

// canRemoveFrom reports whether user may delete/rename src
// (needs write+execute on its parent directory).
func (s *Shell) canRemoveFrom(src string) bool {
	parent, _, err := s.FS.splitParent(s.Cwd, src)
	if err != nil {
		return false
	}
	return CanWrite(parent, s.User) && CanExec(parent, s.User)
}

// canCreateAt reports whether user may create/replace an entry named
// srcName under destination dst (like real cp/mv directory checks).
func (s *Shell) canCreateAt(dst, srcName string) bool {
	if dn, err := s.FS.Lookup(s.Cwd, dst); err == nil && dn.Type == TypeDir {
		if _, exists := dn.Children[srcName]; exists {
			return CanWrite(dn, s.User) && CanExec(dn, s.User)
		}
		return CanWrite(dn, s.User) && CanExec(dn, s.User)
	}
	parent, _, err := s.FS.splitParent(s.Cwd, dst)
	if err != nil {
		return false
	}
	return CanWrite(parent, s.User) && CanExec(parent, s.User)
}

// targetPathFor returns the path cp would actually write to.
func (s *Shell) targetPathFor(dst, srcName string) string {
	if dn, err := s.FS.Lookup(s.Cwd, dst); err == nil && dn.Type == TypeDir {
		return path.Join(dst, srcName)
	}
	return dst
}

func trimSuffix(s, suf string) string {
	if len(s) > len(suf) && s[len(s)-len(suf):] == suf {
		return s[:len(s)-len(suf)]
	}
	return s
}
