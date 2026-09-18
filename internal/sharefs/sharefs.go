// Package sharefs implements a billy.Filesystem confined to one directory
// on the host, with the semantics an NFS export needs.
//
// osfs.BoundOS was the first candidate, but it resolves symlinks in the
// final path component for every operation, so removing or renaming a
// symlink acts on its target. An NFS REMOVE of a symlink must remove the
// link. This package keeps the final component literal for the operations
// that must not follow symlinks, and resolves it for the ones that must.
// Every resolution is scoped to the root, so a symlink cannot lead outside
// the shared directory.
//
// It also implements billy.Change, which BoundOS lacks. Without it go-nfs
// answers every SETATTR with NFS3ERR_NOTSUPP and touch, cp -p, rsync -a and
// Finder copies fail on the client.
package sharefs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	securejoin "github.com/cyphar/filepath-securejoin"
	billy "github.com/go-git/go-billy/v5"
)

// FS is a billy.Filesystem rooted at a host directory.
type FS struct {
	root string
}

// New returns a filesystem confined to root, which must be an absolute path.
func New(root string) *FS {
	return &FS{root: filepath.Clean(root)}
}

var (
	_ billy.Filesystem = (*FS)(nil)
	_ billy.Change     = (*FS)(nil)
	_ billy.Capable    = (*FS)(nil)
)

// Capabilities reports the full default set so callers see a writable fs.
func (fs *FS) Capabilities() billy.Capability { return billy.DefaultCapabilities }

// Root returns the host directory the filesystem is confined to.
func (fs *FS) Root() string { return fs.root }

// Join joins path elements with the OS separator.
func (fs *FS) Join(elem ...string) string { return filepath.Join(elem...) }

// following resolves name fully, following symlinks in every component,
// while keeping the result inside the root.
func (fs *FS) following(name string) (string, error) {
	return securejoin.SecureJoin(fs.root, name)
}

// literal resolves the parent of name, following symlinks, and appends the
// final component unchanged. The result stays inside the root because the
// parent does and the final component contains no separator.
func (fs *FS) literal(name string) (string, error) {
	name = filepath.Clean("/" + name)
	if name == "/" {
		return fs.root, nil
	}
	parent, err := securejoin.SecureJoin(fs.root, filepath.Dir(name))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(name)), nil
}

func (fs *FS) isRoot(name string) bool {
	return filepath.Clean("/"+name) == "/"
}

// Create opens name for writing, truncating it if it exists.
func (fs *FS) Create(name string) (billy.File, error) {
	return fs.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
}

// Open opens name for reading.
func (fs *FS) Open(name string) (billy.File, error) {
	return fs.OpenFile(name, os.O_RDONLY, 0)
}

// OpenFile opens name with the given flags, following symlinks.
func (fs *FS) OpenFile(name string, flag int, perm os.FileMode) (billy.File, error) {
	p, err := fs.following(name)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, flag, perm)
	if err != nil {
		return nil, err
	}
	return &file{File: f, name: name}, nil
}

// Stat returns information about the target of name.
func (fs *FS) Stat(name string) (os.FileInfo, error) {
	p, err := fs.following(name)
	if err != nil {
		return nil, err
	}
	return os.Stat(p)
}

// Lstat returns information about name itself, without following a final
// symlink.
func (fs *FS) Lstat(name string) (os.FileInfo, error) {
	p, err := fs.literal(name)
	if err != nil {
		return nil, err
	}
	return os.Lstat(p)
}

// Rename moves the entry itself, so renaming a symlink moves the link.
func (fs *FS) Rename(from, to string) error {
	if fs.isRoot(from) || fs.isRoot(to) {
		return errRootImmutable
	}
	f, err := fs.literal(from)
	if err != nil {
		return err
	}
	t, err := fs.literal(to)
	if err != nil {
		return err
	}
	return os.Rename(f, t)
}

// Remove deletes the entry itself, so removing a symlink removes the link.
func (fs *FS) Remove(name string) error {
	if fs.isRoot(name) {
		return errRootImmutable
	}
	p, err := fs.literal(name)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// ReadDir lists the directory that name resolves to, without macOS metadata
// files (see isMacMetadata).
func (fs *FS) ReadDir(name string) ([]os.FileInfo, error) {
	p, err := fs.following(name)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	infos := make([]os.FileInfo, 0, len(entries))
	for _, e := range entries {
		if isMacMetadata(e.Name()) {
			continue
		}
		info, err := os.Lstat(filepath.Join(p, e.Name()))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // removed between listing and stat
			}
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// isMacMetadata reports whether name is a file that macOS creates for its
// own bookkeeping on filesystems without extended attributes: AppleDouble
// sidecars that hold another file's metadata, and Finder's view settings.
// They are hidden from listings so other clients do not see a twin of every
// file, but they stay reachable by exact name so a Mac client can still read
// and write its own.
func isMacMetadata(name string) bool {
	return strings.HasPrefix(name, "._") || name == ".DS_Store"
}

// MkdirAll creates the directory that name resolves to and its parents.
func (fs *FS) MkdirAll(name string, perm os.FileMode) error {
	p, err := fs.following(name)
	if err != nil {
		return err
	}
	return os.MkdirAll(p, perm)
}

// Symlink creates link pointing at target. target is stored as given; when
// followed later it is resolved inside the root like any other path.
func (fs *FS) Symlink(target, link string) error {
	if fs.isRoot(link) {
		return errRootImmutable
	}
	p, err := fs.literal(link)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.Symlink(target, p)
}

// Readlink returns the stored target of link.
func (fs *FS) Readlink(link string) (string, error) {
	p, err := fs.literal(link)
	if err != nil {
		return "", err
	}
	return os.Readlink(p)
}

// TempFile creates a new temporary file inside dir.
func (fs *FS) TempFile(dir, prefix string) (billy.File, error) {
	p, err := fs.following(dir)
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(p, prefix)
	if err != nil {
		return nil, err
	}
	name := filepath.Join(filepath.Clean("/"+dir), filepath.Base(f.Name()))
	return &file{File: f, name: name}, nil
}

// Chroot returns a filesystem confined to the directory name resolves to.
func (fs *FS) Chroot(name string) (billy.Filesystem, error) {
	p, err := fs.following(name)
	if err != nil {
		return nil, err
	}
	return New(p), nil
}

// Chmod changes the mode of the target of name.
func (fs *FS) Chmod(name string, mode os.FileMode) error {
	p, err := fs.following(name)
	if err != nil {
		return err
	}
	return os.Chmod(p, mode)
}

// Chtimes changes the access and modification times of the target of name.
func (fs *FS) Chtimes(name string, atime, mtime time.Time) error {
	p, err := fs.following(name)
	if err != nil {
		return err
	}
	return os.Chtimes(p, atime, mtime)
}

// Chown is accepted and ignored. Clients authenticate with AUTH_NULL, so the
// uid they send has no meaning on the sharer, and a real chown would fail
// for any sharer that is not root. Ignoring it lets cp -p and rsync -a
// complete.
func (fs *FS) Chown(name string, uid, gid int) error {
	p, err := fs.following(name)
	if err != nil {
		return err
	}
	_, err = os.Stat(p)
	return err
}

// Lchown is accepted and ignored for the same reason as Chown.
func (fs *FS) Lchown(name string, uid, gid int) error {
	p, err := fs.literal(name)
	if err != nil {
		return err
	}
	_, err = os.Lstat(p)
	return err
}

var errRootImmutable = errors.New("the share root cannot be removed or renamed")

// file is a billy.File over an *os.File.
type file struct {
	*os.File
	name string
}

// Name returns the path inside the share, not the host path.
func (f *file) Name() string { return strings.TrimPrefix(filepath.Clean("/"+f.name), "/") }

func (f *file) Lock() error   { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }
func (f *file) Unlock() error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
