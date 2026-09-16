package rofs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	billy "github.com/go-git/go-billy/v5"

	"github.com/fcsonline/tailmount/internal/sharefs"
)

func newFS(t *testing.T) billy.Filesystem {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	return New(sharefs.New(dir))
}

func TestMutationsAreRejected(t *testing.T) {
	fs := newFS(t)
	cases := map[string]func() error{
		"Create":            func() error { _, err := fs.Create("new"); return err },
		"OpenFile O_WRONLY": func() error { _, err := fs.OpenFile("a.txt", os.O_WRONLY, 0); return err },
		"OpenFile O_RDWR":   func() error { _, err := fs.OpenFile("a.txt", os.O_RDWR, 0); return err },
		"OpenFile O_CREATE": func() error { _, err := fs.OpenFile("new", os.O_RDONLY|os.O_CREATE, 0o644); return err },
		"OpenFile O_TRUNC":  func() error { _, err := fs.OpenFile("a.txt", os.O_RDONLY|os.O_TRUNC, 0); return err },
		"OpenFile O_APPEND": func() error { _, err := fs.OpenFile("a.txt", os.O_APPEND, 0); return err },
		"Rename":            func() error { return fs.Rename("a.txt", "b.txt") },
		"Remove":            func() error { return fs.Remove("a.txt") },
		"MkdirAll":          func() error { return fs.MkdirAll("x/y", 0o755) },
		"Symlink":           func() error { return fs.Symlink("a.txt", "l2") },
		"TempFile":          func() error { _, err := fs.TempFile("", "tmp"); return err },
	}
	for name, fn := range cases {
		if err := fn(); !errors.Is(err, billy.ErrReadOnly) {
			t.Errorf("%s: got %v, want ErrReadOnly", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(fs.Root(), "a.txt")); err != nil {
		t.Fatalf("a.txt should still exist: %v", err)
	}
}

func TestReadsPassThrough(t *testing.T) {
	fs := newFS(t)
	f, err := fs.Open("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := f.Read(buf); err != nil || string(buf) != "hello" {
		t.Fatalf("Read = %q, %v", buf, err)
	}
	if _, err := f.Write([]byte("x")); !errors.Is(err, billy.ErrReadOnly) {
		t.Errorf("Write on read handle = %v, want ErrReadOnly", err)
	}
	if err := f.Truncate(0); !errors.Is(err, billy.ErrReadOnly) {
		t.Errorf("Truncate on read handle = %v, want ErrReadOnly", err)
	}
	f.Close()
	if _, err := fs.OpenFile("a.txt", os.O_RDONLY, 0); err != nil {
		t.Errorf("OpenFile O_RDONLY: %v", err)
	}
	if _, err := fs.Stat("a.txt"); err != nil {
		t.Errorf("Stat: %v", err)
	}
	if _, err := fs.Lstat("link"); err != nil {
		t.Errorf("Lstat: %v", err)
	}
	if target, err := fs.Readlink("link"); err != nil || target != "a.txt" {
		t.Errorf("Readlink = %q, %v", target, err)
	}
	entries, err := fs.ReadDir("/")
	if err != nil || len(entries) != 3 {
		t.Errorf("ReadDir = %d entries, %v", len(entries), err)
	}
}

func TestCapabilitiesAndChange(t *testing.T) {
	fs := newFS(t)
	if billy.CapabilityCheck(fs, billy.WriteCapability) {
		t.Error("read-only fs must not report WriteCapability")
	}
	if !billy.CapabilityCheck(fs, billy.ReadCapability) {
		t.Error("read-only fs must report ReadCapability")
	}
	if _, ok := fs.(billy.Change); ok {
		t.Error("read-only fs must not implement billy.Change")
	}
	sub, err := fs.Chroot("sub")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sub.Create("x"); !errors.Is(err, billy.ErrReadOnly) {
		t.Errorf("Chroot result is writable: %v", err)
	}
}
