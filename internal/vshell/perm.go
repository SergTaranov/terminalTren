package vshell

import (
	"fmt"
	"strconv"
	"strings"
)

func permCmds() []Cmd {
	return []Cmd{
		{
			Name: "chmod",
			Man: `chmod — изменить права доступа
Синтаксис: chmod [-R] РЕЖИМ файл...
  -R  рекурсивно (для каталогов)
РЕЖИМ цифрами: 3 восьмеричные цифры ugo:
  7=rwx 6=rw- 5=r-x 4=r-- 0=---
Пример: chmod 600 secret.txt
РЕЖИМ символами: [ugoa][+-=][rwx]
Примеры: chmod u+x script.sh, chmod go-rw file, chmod a=r file`,
			Run: chmodRun,
		},
		{
			Name: "chown",
			Man: `chown — сменить владельца файла
Синтаксис: chown [-R] [владелец][:группа] файл...
  -R  рекурсивно
Менять владельца может только root: sudo chown user file`,
			Run: chownRun,
		},
		{
			Name: "umask",
			Man: `umask — маска прав по умолчанию
Синтаксис: umask [МАСКА]
Без аргументов печатает текущую маску (обычно 0022).
Новые файлы: 666 - umask, каталоги: 777 - umask.`,
			Run: func(c *Ctx) int {
				s := c.Shell
				_, _, operands, err := parseArgs("", c.Args[1:], nil)
				if err != nil {
					return errPlain(c, err.Error())
				}
				if len(operands) == 0 {
					fmt.Fprintf(c.Stdout, "%04o\n", s.Umask)
					return 0
				}
				m, perr := strconv.ParseUint(operands[0], 8, 32)
				if perr != nil {
					return errPlain(c, "invalid mode '"+operands[0]+"'")
				}
				s.Umask = uint32(m)
				return 0
			},
		},
	}
}

func chmodRun(c *Ctx) int {
	s := c.Shell
	bools, _, operands, err := parseArgs("R", c.Args[1:], map[string]byte{"recursive": 'R'})
	if err != nil {
		return errPlain(c, err.Error())
	}
	if len(operands) < 2 {
		return errPlain(c, "missing operand")
	}
	mode := operands[0]
	code := 0
	for _, p := range operands[1:] {
		// chmod (without -h) follows symlinks and changes the target
		n, lerr := s.FS.Lookup(s.Cwd, p)
		if lerr != nil {
			code = errNode(c, p, lerr)
			continue
		}
		if s.User != "root" && n.Owner != s.User {
			fmt.Fprintf(c.Stderr, "chmod: changing permissions of '%s': Operation not permitted\n", p)
			code = 1
			continue
		}
		apply := func(node *Node) int {
			newPerms, perr := applyMode(mode, node)
			if perr != nil {
				fmt.Fprintf(c.Stderr, "chmod: invalid mode: '%s'\n", mode)
				return 1
			}
			node.Perms = newPerms
			return 0
		}
		if bools['R'] && n.Type == TypeDir {
			_ = s.FS.Walk(s.Cwd, p, func(abs string, wn *Node) error {
				if apply(wn) != 0 {
					code = 1
				}
				return nil
			})
			continue
		}
		if apply(n) != 0 {
			code = 1
		}
	}
	return code
}

// applyMode computes new permission bits from an octal or symbolic mode.
// The node is needed for the conditional execute bit 'X'.
func applyMode(mode string, n *Node) (uint32, error) {
	if v, err := strconv.ParseUint(mode, 8, 32); err == nil && len(mode) <= 4 {
		return uint32(v), nil
	}
	// X grants execute only to directories and already-executable files
	xOk := n.Type == TypeDir || n.Perms&0o111 != 0
	// symbolic: comma-separated [ugoa][+-=][rwxXst]+
	result := n.Perms
	for _, part := range strings.Split(mode, ",") {
		i := 0
		whoSet := ""
		for i < len(part) && strings.IndexByte("ugoa", part[i]) >= 0 {
			whoSet += string(part[i])
			i++
		}
		if i >= len(part) || strings.IndexByte("+-=", part[i]) < 0 {
			return 0, fmt.Errorf("bad mode")
		}
		op := part[i]
		i++
		permStr := part[i:]
		if permStr == "" {
			return 0, fmt.Errorf("bad mode")
		}
		var mask uint32
		for j := 0; j < len(permStr); j++ {
			switch permStr[j] {
			case 'r':
				mask |= 4
			case 'w':
				mask |= 2
			case 'x':
				mask |= 1
			case 'X':
				if xOk {
					mask |= 1
				}
			default:
				return 0, fmt.Errorf("bad mode")
			}
		}
		if whoSet == "" {
			whoSet = "a"
		}
		for k := 0; k < len(whoSet); k++ {
			w := whoSet[k]
			var shift uint
			switch w {
			case 'u':
				shift = 6
			case 'g':
				shift = 3
			case 'o':
				shift = 0
			default: // a
				for _, sh := range []uint{6, 3, 0} {
					result = applyPermOp(result, sh, mask, op)
				}
				continue
			}
			result = applyPermOp(result, shift, mask, op)
		}
	}
	return result, nil
}

func applyPermOp(perms uint32, shift uint, mask uint32, op byte) uint32 {
	cur := perms >> shift & 7
	var next uint32
	switch op {
	case '+':
		next = cur | mask
	case '-':
		next = cur &^ mask
	case '=':
		next = mask
	}
	return perms&^(7<<shift) | next<<shift
}

func chownRun(c *Ctx) int {
	s := c.Shell
	bools, _, operands, err := parseArgs("R", c.Args[1:], map[string]byte{"recursive": 'R'})
	if err != nil {
		return errPlain(c, err.Error())
	}
	if len(operands) < 2 {
		return errPlain(c, "missing operand")
	}
	spec := operands[0]
	owner, group := spec, ""
	if colon := strings.IndexByte(spec, ':'); colon >= 0 {
		owner, group = spec[:colon], spec[colon+1:]
	} else if dot := strings.IndexByte(spec, '.'); dot >= 0 {
		owner, group = spec[:dot], spec[dot+1:]
	}
	if owner == "" && group == "" {
		return errPlain(c, "invalid owner: '"+spec+"'")
	}
	code := 0
	for _, p := range operands[1:] {
		if s.User != "root" {
			fmt.Fprintf(c.Stderr, "chown: changing ownership of '%s': Operation not permitted\n", p)
			code = 1
			continue
		}
		if err := s.FS.Chown(s.Cwd, p, owner, group, bools['R']); err != nil {
			code = errNode(c, p, err)
		}
	}
	return code
}
