package mount

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

// mountArgs builds the Linux mount command. Mounting needs CAP_SYS_ADMIN, so
// the command runs through sudo.
//
// nolock: the kernel would otherwise contact an NLM lock daemon through
// portmap, which the tunnel does not carry, and lock calls would hang.
// soft plus timeo/retrans: a share disappears whenever the sharer stops, and
// a hard mount would leave processes stuck until a forced unmount.
// rsize/wsize: each transfer is a round trip over the tunnel, so use the
// largest size the kernel allows.
func mountArgs(port string, readOnly bool, mp string) []string {
	opts := []string{
		"port=" + port,
		"mountport=" + port,
		"proto=tcp",
		"mountproto=tcp",
		"nfsvers=3",
		"nolock",
		"noacl",
		"soft",
		"timeo=100",
		"retrans=3",
		"rsize=1048576",
		"wsize=1048576",
	}
	if readOnly {
		opts = append(opts, "ro")
	}
	return []string{"sudo", "mount", "-t", "nfs", "-o", strings.Join(opts, ","), "127.0.0.1:/", mp}
}

func umountArgs(mp string) [][]string {
	return [][]string{
		{"sudo", "umount", mp},
		{"sudo", "umount", "-f", "-l", mp},
	}
}

func manualUnmountHint(mp string) string {
	return "sudo umount -f -l " + mp
}

// checkPrereqs verifies the NFS mount helper is installed before asking for
// a sudo password.
func checkPrereqs() error {
	if _, err := exec.LookPath("mount.nfs"); err == nil {
		return nil
	}
	for _, p := range []string{"/sbin/mount.nfs", "/usr/sbin/mount.nfs"} {
		if _, err := os.Stat(p); err == nil {
			return nil
		}
	}
	return errors.New("mount.nfs not found; install the NFS client (Debian/Ubuntu: sudo apt install nfs-common, Fedora: sudo dnf install nfs-utils)")
}

// checkOwner is a no-op: Linux mounts run as root through sudo.
func checkOwner(string, os.FileInfo) error { return nil }
