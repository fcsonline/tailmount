// Package rofs wraps a billy.Filesystem so that every mutating operation
// fails with billy.ErrReadOnly.
//
// The wrapper also reports capabilities without WriteCapability. go-nfs
// consults that to answer NFS3ERR_ROFS and to strip write bits from ACCESS
// replies, which is what makes clients see a proper read-only export instead
// of scattered permission errors.
package rofs

import (
	"os"

	billy "github.com/go-git/go-billy/v5"
)

const writeFlags = os.O_WRONLY | os.O_RDWR | os.O_APPEND | os.O_CREATE | os.O_TRUNC

// FS is a read-only view over another filesystem.
type FS struct {
	inner billy.Filesystem
}

// New returns a read-only view of inner.
func New(inner billy.Filesystem) billy.Filesystem {
	return &FS{inner: inner}
}

// Capabilities omits WriteCapability so callers that check capabilities
// treat the filesystem as read-only before attempting a write.
func (fs *FS) Capabilities() billy.Capability {
	return billy.ReadCapability | billy.SeekCapability
}

func (fs *FS) Create(string) (billy.File, error) { return nil, billy.ErrReadOnly }

func (fs *FS) Open(filename string) (billy.File, error) {
	f, err := fs.inner.Open(filename)
	if err != nil {
		return nil, err
	}
	return &file{File: f}, nil
}

func (fs *FS) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	if flag&writeFlags != 0 {
		return nil, billy.ErrReadOnly
	}
	f, err := fs.inner.OpenFile(filename, flag, perm)
	if err != nil {
		return nil, err
	}
	return &file{File: f}, nil
}

func (fs *FS) Stat(filename string) (os.FileInfo, error) { return fs.inner.Stat(filename) }
func (fs *FS) Rename(string, string) error               { return billy.ErrReadOnly }
func (fs *FS) Remove(string) error                       { return billy.ErrReadOnly }
func (fs *FS) Join(elem ...string) string                { return fs.inner.Join(elem...) }

func (fs *FS) TempFile(string, string) (billy.File, error) { return nil, billy.ErrReadOnly }

func (fs *FS) ReadDir(path string) ([]os.FileInfo, error) { return fs.inner.ReadDir(path) }
func (fs *FS) MkdirAll(string, os.FileMode) error         { return billy.ErrReadOnly }

func (fs *FS) Lstat(filename string) (os.FileInfo, error) { return fs.inner.Lstat(filename) }
func (fs *FS) Symlink(string, string) error               { return billy.ErrReadOnly }
func (fs *FS) Readlink(link string) (string, error)       { return fs.inner.Readlink(link) }

func (fs *FS) Chroot(path string) (billy.Filesystem, error) {
	sub, err := fs.inner.Chroot(path)
	if err != nil {
		return nil, err
	}
	return New(sub), nil
}

func (fs *FS) Root() string { return fs.inner.Root() }

// file blocks writes on a handle that was opened read-only, so a caller that
// bypasses the open flags still cannot modify content.
type file struct {
	billy.File
}

func (f *file) Write([]byte) (int, error)          { return 0, billy.ErrReadOnly }
func (f *file) Truncate(int64) error               { return billy.ErrReadOnly }
func (f *file) Lock() error                        { return billy.ErrReadOnly }
func (f *file) Unlock() error                      { return billy.ErrReadOnly }
func (f *file) WriteAt([]byte, int64) (int, error) { return 0, billy.ErrReadOnly }

var _ interface {
	WriteAt([]byte, int64) (int, error)
} = (*file)(nil)
