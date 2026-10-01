// Package vshell implements a virtual Linux shell: an in-memory filesystem,
// a command interpreter with pipes and redirections, and a registry of
// emulated coreutils commands.
package vshell

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// NodeType distinguishes files, directories and symbolic links.
type NodeType int

const (
	TypeFile NodeType = iota
	TypeDir
	TypeSymlink
)

// Common filesystem errors, worded like real Linux messages.
var (
	ErrNotExist    = errors.New("No such file or directory")
	ErrExist       = errors.New("File exists")
	ErrNotDir      = errors.New("Not a directory")
	ErrIsDir       = errors.New("Is a directory")
	ErrNotEmpty    = errors.New("Directory not empty")
	ErrBadTarget   = errors.New("cannot overwrite directory with non-directory")
	ErrSymlinkLoop = errors.New("Too many levels of symbolic links")
)

// Node is one entry of the virtual filesystem.
type Node struct {
	Name     string
	Type     NodeType
	Content  string
	Children map[string]*Node
	Perms    uint32 // octal permission bits, e.g. 0o644
	Owner    string
	Group    string
	Target   string // symlink target, relative or absolute
	MTime    time.Time
}

// IsDir reports whether the node is a directory.
func (n *Node) IsDir() bool { return n.Type == TypeDir }

// Size returns the file size in bytes (directory size is 4096 like on ext4).
func (n *Node) Size() int {
	if n.Type == TypeDir {
		return 4096
	}
	return len(n.Content)
}

// FS is the virtual filesystem rooted at "/".
type FS struct {
	root *Node
	Home string // home directory of the current user, e.g. /home/user
	Now  func() time.Time
}

// NewFS creates a filesystem with the standard skeleton and /home/user as home.
func NewFS() *FS {
	now := time.Now
	fs := &FS{
		Home: "/home/user",
		Now:  now,
	}
	fs.root = &Node{
		Name:     "/",
		Type:     TypeDir,
		Children: map[string]*Node{},
		Perms:    0o755,
		Owner:    "root",
		Group:    "root",
		MTime:    now(),
	}
	for _, d := range []struct {
		p     string
		owner string
	}{
		{"/home", "root"},
		{"/home/user", "user"},
		{"/tmp", "root"},
		{"/etc", "root"},
		{"/var", "root"},
		{"/var/log", "root"},
		{"/usr", "root"},
		{"/usr/bin", "root"},
		{"/opt", "root"},
	} {
		_ = fs.MkdirAll("/", d.p, 0o755, d.owner, d.owner)
	}
	return fs
}

// Root returns the root node.
func (fs *FS) Root() *Node { return fs.root }

// AbsPath resolves p against cwd, expanding ~ to the home directory and
// cleaning the result. The returned path is always absolute.
func (fs *FS) AbsPath(cwd, p string) string {
	switch {
	case p == "" || p == ".":
		return path.Clean(cwd)
	case p == "~":
		return fs.Home
	case strings.HasPrefix(p, "~/"):
		p = fs.Home + "/" + p[2:]
	case !strings.HasPrefix(p, "/"):
		p = cwd + "/" + p
	}
	return path.Clean(p)
}

func (fs *FS) splitParent(cwd, p string) (*Node, string, error) {
	abs := fs.AbsPath(cwd, p)
	if abs == "/" {
		return fs.root, "", nil
	}
	dir, base := path.Split(abs)
	dir = path.Clean(dir)
	parent, err := fs.lookupAbs(cwd, dir, false)
	if err != nil {
		return nil, "", err
	}
	if parent.Type != TypeDir {
		return nil, "", ErrNotDir
	}
	return parent, base, nil
}

// lookupAbs walks abs from root, resolving symlinks unless noFollow is set
// for the final component.
func (fs *FS) lookupAbs(cwd, abs string, noFollow bool) (*Node, error) {
	return fs.lookupDepth(cwd, abs, noFollow, 0)
}

func (fs *FS) lookupDepth(cwd, abs string, noFollow bool, depth int) (*Node, error) {
	if depth > 40 {
		return nil, ErrSymlinkLoop // like ELOOP in Linux
	}
	cur := fs.root
	parts := strings.Split(strings.Trim(path.Clean(abs), "/"), "/")
	for i, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if cur.Type != TypeDir {
			return nil, ErrNotDir
		}
		next, ok := cur.Children[part]
		if !ok {
			return nil, ErrNotExist
		}
		last := i == len(parts)-1
		if next.Type == TypeSymlink && !(noFollow && last) {
			target := next.Target
			if !strings.HasPrefix(target, "/") {
				target = symlinkDir(abs, i) + "/" + target
			}
			res, err := fs.lookupDepth(cwd, fs.AbsPath(cwd, target), false, depth+1)
			if err != nil {
				return nil, err
			}
			next = res
		}
		cur = next
	}
	return cur, nil
}

// symlinkDir returns the directory part of the path up to component i.
func symlinkDir(abs string, i int) string {
	parts := strings.Split(strings.Trim(path.Clean(abs), "/"), "/")
	return "/" + strings.Join(parts[:i], "/")
}

// Lookup resolves p (following symlinks) relative to cwd.
func (fs *FS) Lookup(cwd, p string) (*Node, error) {
	return fs.lookupAbs(cwd, fs.AbsPath(cwd, p), false)
}

// LookupNoFollow resolves p but does not follow a symlink as the final component.
func (fs *FS) LookupNoFollow(cwd, p string) (*Node, error) {
	return fs.lookupAbs(cwd, fs.AbsPath(cwd, p), true)
}

// Mkdir creates a single directory. Parent must exist.
func (fs *FS) Mkdir(cwd, p string, perms uint32, owner, group string) error {
	parent, base, err := fs.splitParent(cwd, p)
	if err != nil {
		return err
	}
	if base == "" {
		return ErrExist
	}
	if _, exists := parent.Children[base]; exists {
		return ErrExist
	}
	parent.Children[base] = &Node{
		Name:     base,
		Type:     TypeDir,
		Children: map[string]*Node{},
		Perms:    perms,
		Owner:    owner,
		Group:    group,
		MTime:    fs.Now(),
	}
	parent.MTime = fs.Now()
	return nil
}

// MkdirAll creates p and any missing parents.
func (fs *FS) MkdirAll(cwd, p string, perms uint32, owner, group string) error {
	abs := fs.AbsPath(cwd, p)
	if abs == "/" {
		return nil
	}
	parts := strings.Split(strings.Trim(abs, "/"), "/")
	cur := fs.root
	for _, part := range parts {
		next, ok := cur.Children[part]
		if !ok {
			child := &Node{
				Name:     part,
				Type:     TypeDir,
				Children: map[string]*Node{},
				Perms:    perms,
				Owner:    owner,
				Group:    group,
				MTime:    fs.Now(),
			}
			cur.Children[part] = child
			cur = child
			continue
		}
		if next.Type != TypeDir {
			return ErrNotDir
		}
		cur = next
	}
	return nil
}

// Create creates or truncates a file and returns it.
func (fs *FS) Create(cwd, p string, perms uint32, owner, group string) (*Node, error) {
	parent, base, err := fs.splitParent(cwd, p)
	if err != nil {
		return nil, err
	}
	if base == "" {
		return nil, ErrIsDir
	}
	if existing, ok := parent.Children[base]; ok {
		switch existing.Type {
		case TypeDir:
			return nil, ErrIsDir
		case TypeSymlink:
			// writing through a symlink touches the target, not the link
			target := existing.Target
			if !strings.HasPrefix(target, "/") {
				target = path.Join(path.Dir(fs.AbsPath(cwd, p)), target)
			}
			return fs.Create(cwd, path.Clean(target), perms, owner, group)
		}
		existing.Content = ""
		existing.MTime = fs.Now()
		return existing, nil
	}
	n := &Node{
		Name:  base,
		Type:  TypeFile,
		Perms: perms,
		Owner: owner,
		Group: group,
		MTime: fs.Now(),
	}
	parent.Children[base] = n
	parent.MTime = fs.Now()
	return n, nil
}

// WriteFile creates (or truncates) p and writes content.
func (fs *FS) WriteFile(cwd, p, content string, perms uint32, owner, group string) error {
	n, err := fs.Create(cwd, p, perms, owner, group)
	if err != nil {
		return err
	}
	n.Content = content
	n.MTime = fs.Now()
	return nil
}

// AppendFile creates (or opens) p and appends content.
func (fs *FS) AppendFile(cwd, p, content string, perms uint32, owner, group string) error {
	n, err := fs.Lookup(cwd, p)
	if err != nil {
		n2, cerr := fs.Create(cwd, p, perms, owner, group)
		if cerr != nil {
			return cerr
		}
		n2.Content = content
		return nil
	}
	if n.Type == TypeDir {
		return ErrIsDir
	}
	n.Content += content
	n.MTime = fs.Now()
	return nil
}

// ReadFile returns the content of a regular file (following symlinks).
func (fs *FS) ReadFile(cwd, p string) (string, error) {
	n, err := fs.Lookup(cwd, p)
	if err != nil {
		return "", err
	}
	if n.Type == TypeDir {
		return "", ErrIsDir
	}
	return n.Content, nil
}

// Symlink creates a symbolic link at p pointing to target.
func (fs *FS) Symlink(cwd, p, target string, owner, group string) error {
	parent, base, err := fs.splitParent(cwd, p)
	if err != nil {
		return err
	}
	if base == "" {
		return ErrExist
	}
	if _, ok := parent.Children[base]; ok {
		return ErrExist
	}
	parent.Children[base] = &Node{
		Name:   base,
		Type:   TypeSymlink,
		Target: target,
		Perms:  0o777,
		Owner:  owner,
		Group:  group,
		MTime:  fs.Now(),
	}
	return nil
}

// Remove deletes p; a directory must be empty unless recursive is set.
func (fs *FS) Remove(cwd, p string, recursive bool) error {
	parent, base, err := fs.splitParent(cwd, p)
	if err != nil {
		return err
	}
	if base == "" {
		return errors.New("cannot remove '/'")
	}
	n, ok := parent.Children[base]
	if !ok {
		return ErrNotExist
	}
	if n.Type == TypeDir && !recursive && len(n.Children) > 0 {
		return ErrNotEmpty
	}
	delete(parent.Children, base)
	parent.MTime = fs.Now()
	return nil
}

// Move renames src to dst. If dst is an existing directory, src moves into it.
func (fs *FS) Move(cwd, src, dst string) error {
	srcParent, srcBase, err := fs.splitParent(cwd, src)
	if err != nil {
		return err
	}
	if srcBase == "" {
		return errors.New("cannot move '/'")
	}
	node, ok := srcParent.Children[srcBase]
	if !ok {
		return ErrNotExist
	}
	srcAbs, dstAbs := fs.AbsPath(cwd, src), fs.AbsPath(cwd, dst)
	if srcAbs == dstAbs {
		return fmt.Errorf("'%s' and '%s' are the same file", src, dst)
	}
	if node.Type == TypeDir && strings.HasPrefix(dstAbs+"/", srcAbs+"/") {
		return errors.New("cannot move a directory into itself")
	}
	dstNode, derr := fs.Lookup(cwd, dst)
	if derr == nil && dstNode.Type == TypeDir {
		name := path.Base(srcBase)
		if _, exists := dstNode.Children[name]; exists {
			return ErrExist
		}
		delete(srcParent.Children, srcBase)
		dstNode.Children[name] = node
		node.Name = name
		return nil
	}
	dstParent, dstBase, err := fs.splitParent(cwd, dst)
	if err != nil {
		return err
	}
	if dstBase == "" {
		return ErrIsDir
	}
	if node.Type == TypeDir {
		if existing, ok := dstParent.Children[dstBase]; ok && existing.Type != TypeDir {
			return fmt.Errorf("cannot overwrite non-directory '%s' with directory '%s'", dst, src)
		}
	}
	delete(srcParent.Children, srcBase)
	node.Name = dstBase
	dstParent.Children[dstBase] = node
	return nil
}

// Copy copies src to dst. If dst is an existing directory, src is copied into
// it. Directories require recursive. New nodes get the source mode masked by
// umask, like cp without -p.
func (fs *FS) Copy(cwd, src, dst string, recursive bool, owner, group string, umask uint32) error {
	srcAbs, dstAbs := fs.AbsPath(cwd, src), fs.AbsPath(cwd, dst)
	if srcAbs == dstAbs {
		return fmt.Errorf("'%s' and '%s' are the same file", src, dst)
	}
	srcNode, err := fs.LookupNoFollow(cwd, src)
	if err != nil {
		return err
	}
	if srcNode.Type == TypeDir && strings.HasPrefix(dstAbs+"/", srcAbs+"/") {
		return errors.New("cannot copy a directory into itself")
	}
	if dstNode, derr := fs.Lookup(cwd, dst); derr == nil && dstNode.Type == TypeDir {
		dstAbs = path.Join(dstAbs, path.Base(srcAbs))
	}
	return fs.copyNode(srcNode, dstAbs, recursive, owner, group, umask)
}

func (fs *FS) copyNode(src *Node, dstAbs string, recursive bool, owner, group string, umask uint32) error {
	switch src.Type {
	case TypeDir:
		if !recursive {
			return errors.New("omitting directory")
		}
		if err := fs.MkdirAll("/", dstAbs, src.Perms&^umask, owner, group); err != nil {
			return err
		}
		for _, name := range sortedNames(src.Children) {
			child := src.Children[name]
			if err := fs.copyNode(child, path.Join(dstAbs, name), recursive, owner, group, umask); err != nil {
				return err
			}
		}
		return nil
	case TypeSymlink:
		parent, base, err := fs.splitParent("/", dstAbs)
		if err != nil {
			return err
		}
		parent.Children[base] = &Node{
			Name: base, Type: TypeSymlink, Target: src.Target,
			Perms: 0o777, Owner: owner, Group: group, MTime: fs.Now(),
		}
		return nil
	default:
		resolved, err := fs.Lookup("/", fs.AbsPath("/", dstAbs))
		if err == nil && resolved.IsDir() {
			return ErrIsDir
		}
		return fs.WriteFile("/", dstAbs, src.Content, src.Perms&^umask, owner, group)
	}
}

// Chmod sets permission bits of p (following symlinks).
func (fs *FS) Chmod(cwd, p string, perms uint32) error {
	n, err := fs.Lookup(cwd, p)
	if err != nil {
		return err
	}
	n.Perms = perms
	n.MTime = fs.Now()
	return nil
}

// Chown sets owner/group of p, optionally recursive. Like real chown it
// follows symlinks (no -h support).
func (fs *FS) Chown(cwd, p, owner, group string, recursive bool) error {
	n, err := fs.Lookup(cwd, p)
	if err != nil {
		return err
	}
	return fs.chownNode(fs.AbsPath(cwd, p), n, owner, group, recursive)
}

func (fs *FS) chownNode(abs string, n *Node, owner, group string, recursive bool) error {
	if owner != "" {
		n.Owner = owner
	}
	if group != "" {
		n.Group = group
	}
	if n.Type == TypeDir && recursive {
		for _, name := range sortedNames(n.Children) {
			if err := fs.chownNode(path.Join(abs, name), n.Children[name], owner, group, recursive); err != nil {
				return err
			}
		}
	}
	return nil
}

// Walk calls fn for abs and all its descendants (symlinks are not followed).
func (fs *FS) Walk(cwd, p string, fn func(abs string, n *Node) error) error {
	start, err := fs.LookupNoFollow(cwd, p)
	if err != nil {
		return err
	}
	return fs.walkNode(fs.AbsPath(cwd, p), start, fn)
}

func (fs *FS) walkNode(abs string, n *Node, fn func(abs string, n *Node) error) error {
	if err := fn(abs, n); err != nil {
		return err
	}
	if n.Type != TypeDir {
		return nil
	}
	for _, name := range sortedNames(n.Children) {
		if err := fs.walkNode(path.Join(abs, name), n.Children[name], fn); err != nil {
			return err
		}
	}
	return nil
}

// DirSize returns the total byte size of a subtree (files only, plus dir blocks).
func (fs *FS) DirSize(abs string, n *Node) int {
	total := 0
	_ = fs.walkNode(abs, n, func(_ string, x *Node) error {
		total += x.Size()
		return nil
	})
	return total
}

func sortedNames(m map[string]*Node) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ListDir returns the children of directory p sorted by name.
func (fs *FS) ListDir(cwd, p string) ([]*Node, error) {
	n, err := fs.Lookup(cwd, p)
	if err != nil {
		return nil, err
	}
	if n.Type != TypeDir {
		return nil, ErrNotDir
	}
	out := make([]*Node, 0, len(n.Children))
	for _, name := range sortedNames(n.Children) {
		out = append(out, n.Children[name])
	}
	return out, nil
}
