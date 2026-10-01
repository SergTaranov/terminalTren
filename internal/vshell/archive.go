package vshell

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// tarEntry is one archived node in the trainer's tar format (JSON-based).
type tarEntry struct {
	Path    string `json:"path"`
	Type    string `json:"type"` // f, d, l
	Perms   uint32 `json:"perms"`
	Owner   string `json:"owner"`
	Content string `json:"content,omitempty"`
	Target  string `json:"target,omitempty"`
}

func archiveCmds() []Cmd {
	return []Cmd{
		{
			Name: "tar",
			Man: `tar — архиватор
Синтаксис: tar РЕЖИМ -f АРХИВ [файлы...]
  -c  создать архив
  -x  распаковать
  -t  показать содержимое
  -z  gzip-сжатие (.tar.gz)
  -v  подробно
  -f  имя файла архива (обязательно)
Примеры: tar -czf backup.tar.gz docs
         tar -tzf backup.tar.gz
         tar -xzf backup.tar.gz`,
			Run: tarRun,
		},
		{
			Name: "gzip",
			Man: `gzip — сжать файл
Синтаксис: gzip [-k] [-c] файл...
Создаёт файл.gz, оригинал удаляется.
  -k  сохранить оригинал
  -c  вывести результат на stdout
gzip -d / gunzip — распаковать.`,
			Run: func(c *Ctx) int { return gzipRun(c, false) },
		},
		{
			Name: "gunzip",
			Man: `gunzip — распаковать .gz файл
Синтаксис: gunzip [-k] [-c] файл.gz`,
			Run: func(c *Ctx) int { return gzipRun(c, true) },
		},
		{
			Name: "zcat",
			Man: `zcat — посмотреть содержимое .gz файла
Синтаксис: zcat файл.gz`,
			Run: func(c *Ctx) int {
				s := c.Shell
				_, _, operands, err := parseArgs("", c.Args[1:], nil)
				if err != nil {
					return errPlain(c, err.Error())
				}
				code := 0
				for _, p := range operands {
					content, derr := gunzipFile(s, p)
					if derr != nil {
						code = errNode(c, p, derr)
						continue
					}
					fmt.Fprint(c.Stdout, content)
				}
				return code
			},
		},
	}
}

func tarRun(c *Ctx) int {
	s := c.Shell
	bools, vals, operands, err := parseArgs("czxvtf:r", c.Args[1:], map[string]byte{
		"create": 'c', "extract": 'x', "list": 't', "file": 'f',
		"gzip": 'z', "verbose": 'v',
	})
	if err != nil {
		return errPlain(c, err.Error())
	}
	archive := vals['f']
	if archive == "" {
		return errPlain(c, "you must specify one of the '-Acdtrux' options with '-f' archive name")
	}

	switch {
	case bools['c']:
		if len(operands) == 0 {
			return errPlain(c, "cowardly refusing to create an empty archive")
		}
		var entries []tarEntry
		for _, p := range operands {
			// members keep the operand's path (minus a leading /), like
			// real tar: `tar -cf a.tar docs/f.txt` stores docs/f.txt
			operandAbs := s.FS.AbsPath(s.Cwd, p)
			memberBase := path.Clean(p)
			if strings.HasPrefix(memberBase, "/") {
				memberBase = strings.TrimPrefix(memberBase, "/")
			}
			werr := s.FS.Walk(s.Cwd, p, func(abs string, n *Node) error {
				member := memberBase
				if abs != operandAbs {
					member = memberBase + strings.TrimPrefix(abs, operandAbs)
				}
				e := tarEntry{Path: member, Perms: n.Perms, Owner: n.Owner}
				switch n.Type {
				case TypeDir:
					e.Type = "d"
				case TypeSymlink:
					e.Type = "l"
					e.Target = n.Target
				default:
					e.Type = "f"
					e.Content = n.Content
				}
				entries = append(entries, e)
				if bools['v'] {
					fmt.Fprintln(c.Stdout, member)
				}
				return nil
			})
			if werr != nil {
				return errNode(c, p, werr)
			}
		}
		data, _ := json.Marshal(entries)
		if bools['z'] {
			data = []byte(gzipBytes(data))
		}
		if werr := s.FS.WriteFile(s.Cwd, archive, string(data), 0o644&^s.Umask, s.User, s.User); werr != nil {
			return errNode(c, archive, werr)
		}
		return 0

	case bools['t']:
		content, rerr := s.FS.ReadFile(s.Cwd, archive)
		if rerr != nil {
			return errNode(c, archive, rerr)
		}
		entries, derr := decodeTar(content, bools['z'])
		if derr != nil {
			return errPlain(c, archive+": "+derr.Error())
		}
		for _, e := range entries {
			fmt.Fprintln(c.Stdout, e.Path)
		}
		return 0

	case bools['x']:
		content, rerr := s.FS.ReadFile(s.Cwd, archive)
		if rerr != nil {
			return errNode(c, archive, rerr)
		}
		entries, derr := decodeTar(content, bools['z'])
		if derr != nil {
			return errPlain(c, archive+": "+derr.Error())
		}
		code := 0
		for _, e := range entries {
			switch e.Type {
			case "d":
				_ = s.FS.MkdirAll(s.Cwd, e.Path, e.Perms, e.Owner, e.Owner)
			case "l":
				_ = s.FS.MkdirAll(s.Cwd, path.Dir(e.Path), 0o755, "root", "root")
				_ = s.FS.Symlink(s.Cwd, e.Path, e.Target, e.Owner, e.Owner)
			default:
				_ = s.FS.MkdirAll(s.Cwd, path.Dir(e.Path), 0o755, "root", "root")
				if werr := s.FS.WriteFile(s.Cwd, e.Path, e.Content, e.Perms, e.Owner, e.Owner); werr != nil {
					fmt.Fprintf(c.Stderr, "tar: %s: %s\n", e.Path, werr)
					code = 2
				}
			}
			if bools['v'] {
				fmt.Fprintln(c.Stdout, e.Path)
			}
		}
		return code
	}
	return errPlain(c, "you must specify one of the '-Acdtrux' options")
}

func decodeTar(content string, z bool) ([]tarEntry, error) {
	data := []byte(content)
	// real tar detects gzip on read: `tar -tf x.tar.gz` works without -z
	if z || len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		var err error
		data, err = gunzipBytes(data)
		if err != nil {
			return nil, err
		}
	}
	var entries []tarEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("not a tar archive (trainer format)")
	}
	return entries, nil
}

func gzipRun(c *Ctx, decompress bool) int {
	s := c.Shell
	bools, _, operands, err := parseArgs("dkc", c.Args[1:], map[string]byte{
		"decompress": 'd', "keep": 'k', "stdout": 'c',
	})
	if err != nil {
		return errPlain(c, err.Error())
	}
	if bools['d'] {
		decompress = true
	}
	if len(operands) == 0 {
		return errPlain(c, "missing operand")
	}
	code := 0
	for _, p := range operands {
		if decompress {
			content, derr := gunzipFile(s, p)
			if derr != nil {
				code = errNode(c, p, derr)
				continue
			}
			src, _ := s.FS.Lookup(s.Cwd, p)
			perms := uint32(0o644 &^ s.Umask)
			if src != nil {
				perms = src.Perms // gunzip restores the archived mode
			}
			if bools['c'] {
				fmt.Fprint(c.Stdout, content)
				continue
			}
			out := strings.TrimSuffix(p, ".gz")
			if werr := s.FS.WriteFile(s.Cwd, out, content, perms, s.User, s.User); werr != nil {
				code = errNode(c, out, werr)
				continue
			}
			if !bools['k'] {
				_ = s.FS.Remove(s.Cwd, p, false)
			}
			continue
		}
		content, rerr := s.FS.ReadFile(s.Cwd, p)
		if rerr != nil {
			code = errNode(c, p, rerr)
			continue
		}
		src, _ := s.FS.LookupNoFollow(s.Cwd, p)
		perms := uint32(0o644 &^ s.Umask)
		if src != nil {
			perms = src.Perms // gzip preserves the original mode
		}
		if bools['c'] {
			fmt.Fprint(c.Stdout, gzipBytes([]byte(content)))
			continue
		}
		if werr := s.FS.WriteFile(s.Cwd, p+".gz", string(gzipBytes([]byte(content))), perms, s.User, s.User); werr != nil {
			code = errNode(c, p+".gz", werr)
			continue
		}
		if !bools['k'] {
			_ = s.FS.Remove(s.Cwd, p, false)
		}
	}
	return code
}

func gunzipFile(s *Shell, p string) (string, error) {
	if !strings.HasSuffix(p, ".gz") {
		return "", fmt.Errorf("unknown suffix -- ignored")
	}
	content, err := s.FS.ReadFile(s.Cwd, p)
	if err != nil {
		return "", err
	}
	data, err := gunzipBytes([]byte(content))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func gzipBytes(data []byte) string {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(data)
	_ = w.Close()
	return buf.String()
}

func gunzipBytes(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if _, err := out.ReadFrom(r); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
