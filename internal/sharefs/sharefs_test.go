package sharefs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	billy "github.com/go-git/go-billy/v5"
)

// setup builds:
//
//	root/a.txt
//	root/sub/
//	root/lnk -> a.txt
//	root/escape -> <outside>        (directory outside the root)
//	root/abs -> <outside>/secret    (absolute link to a file outside)
//	outside/secret
func setup(t *testing.T) (*FS, string, string) {
	t.Helper()
	root := t.TempDir()
	outside := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644))
	must(os.Mkdir(filepath.Join(root, "sub"), 0o755))
	must(os.Symlink("a.txt", filepath.Join(root, "lnk")))
	must(os.Symlink(outside, filepath.Join(root, "escape")))
	must(os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0o644))
	must(os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "abs")))
	return New(root), root, outside
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func TestRemoveSymlinkRemovesLinkNotTarget(t *testing.T) {
	fs, root, _ := setup(t)
	if err := fs.Remove("lnk"); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(root, "lnk")) {
		t.Error("link still exists")
	}
	if !exists(filepath.Join(root, "a.txt")) {
		t.Error("target was removed")
	}
}

func TestRenameSymlinkMovesLink(t *testing.T) {
	fs, root, _ := setup(t)
	if err := fs.Rename("lnk", "sub/lnk2"); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(filepath.Join(root, "sub", "lnk2")); err != nil || target != "a.txt" {
		t.Errorf("moved link = %q, %v", target, err)
	}
	if !exists(filepath.Join(root, "a.txt")) {
		t.Error("target was moved")
	}
}

func TestOpenFollowsSymlinkInsideRoot(t *testing.T) {
	fs, _, _ := setup(t)
	f, err := fs.Open("lnk")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, 5)
	if _, err := f.Read(buf); err != nil || string(buf) != "hello" {
		t.Fatalf("Read = %q, %v", buf, err)
	}
	if f.Name() != "lnk" {
		t.Errorf("Name = %q", f.Name())
	}
}

func TestSymlinksCannotEscapeRoot(t *testing.T) {
	fs, _, outside := setup(t)
	for _, name := range []string{"escape/secret", "abs"} {
		if _, err := fs.Open(name); err == nil {
			t.Errorf("Open(%q) succeeded, must not reach outside the root", name)
		}
		if _, err := fs.Stat(name); err == nil {
			t.Errorf("Stat(%q) succeeded, must not reach outside the root", name)
		}
		if err := fs.Chmod(name, 0o600); err == nil {
			t.Errorf("Chmod(%q) succeeded, must not reach outside the root", name)
		}
	}
	if err := fs.Remove("escape/secret"); err == nil {
		t.Error("Remove through an escaping symlink succeeded")
	}
	if _, err := fs.Open("../../../../etc/passwd"); err == nil {
		t.Error("Open with .. escaped the root")
	}
	st, _ := os.Stat(filepath.Join(outside, "secret"))
	if st.Mode().Perm() != 0o644 {
		t.Error("outside file was modified")
	}
	// Lstat and Readlink on the escaping link itself are fine: the link lives inside.
	if _, err := fs.Lstat("abs"); err != nil {
		t.Errorf("Lstat(abs): %v", err)
	}
	if target, err := fs.Readlink("abs"); err != nil || target != filepath.Join(outside, "secret") {
		t.Errorf("Readlink(abs) = %q, %v", target, err)
	}
	// Removing the escaping link removes the link only.
	if err := fs.Remove("abs"); err != nil {
		t.Errorf("Remove(abs): %v", err)
	}
	if !exists(filepath.Join(outside, "secret")) {
		t.Error("outside target was removed")
	}
}

func TestChangeOperations(t *testing.T) {
	fs, root, _ := setup(t)
	if err := fs.Chmod("a.txt", 0o600); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(filepath.Join(root, "a.txt"))
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", st.Mode().Perm())
	}
	when := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := fs.Chtimes("/a.txt", when, when); err != nil {
		t.Fatal(err)
	}
	st, _ = os.Stat(filepath.Join(root, "a.txt"))
	if !st.ModTime().Equal(when) {
		t.Fatalf("mtime = %v, want %v", st.ModTime(), when)
	}
	if err := fs.Chown("a.txt", 12345, 12345); err != nil {
		t.Errorf("Chown should be a no-op, got %v", err)
	}
	if err := fs.Lchown("lnk", 12345, 12345); err != nil {
		t.Errorf("Lchown should be a no-op, got %v", err)
	}
	if err := fs.Chown("missing", 1, 1); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Chown(missing) = %v, want ErrNotExist", err)
	}
	if !billy.CapabilityCheck(fs, billy.WriteCapability) {
		t.Error("expected WriteCapability")
	}
}

func TestDirectoryOperations(t *testing.T) {
	fs, root, _ := setup(t)
	if err := fs.MkdirAll("x/y/z", 0o755); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(root, "x", "y", "z")) {
		t.Error("MkdirAll did not create the tree")
	}
	f, err := fs.Create("x/y/z/new.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("data"))
	f.Close()
	infos, err := fs.ReadDir("x/y/z")
	if err != nil || len(infos) != 1 || infos[0].Name() != "new.txt" {
		t.Fatalf("ReadDir = %v, %v", infos, err)
	}
	infos, err = fs.ReadDir("/")
	if err != nil {
		t.Fatal(err)
	}
	var sawLink bool
	for _, i := range infos {
		if i.Name() == "lnk" && i.Mode()&os.ModeSymlink != 0 {
			sawLink = true
		}
	}
	if !sawLink {
		t.Error("ReadDir must report symlinks as symlinks")
	}
	if err := fs.Symlink("a.txt", "sub/deep/l"); err != nil {
		t.Fatal(err)
	}
	if target, err := fs.Readlink("sub/deep/l"); err != nil || target != "a.txt" {
		t.Errorf("Readlink = %q, %v", target, err)
	}
	tf, err := fs.TempFile("sub", "tmp")
	if err != nil {
		t.Fatal(err)
	}
	tf.Close()
	if filepath.Dir(tf.Name()) != "sub" {
		t.Errorf("TempFile name = %q, want it under sub/", tf.Name())
	}
	sub, err := fs.Chroot("sub")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Root() != filepath.Join(root, "sub") {
		t.Errorf("Chroot root = %q", sub.Root())
	}
	for _, name := range []string{"/", ".", ""} {
		if err := fs.Remove(name); err == nil {
			t.Errorf("Remove(%q) of the root succeeded", name)
		}
	}
}
