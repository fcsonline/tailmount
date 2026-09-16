package mount

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// mountArgs builds the macOS mount command.
//
// locallocks: the kernel would otherwise contact an NLM lock daemon through
// portmap, which the tunnel does not carry, and lock calls would hang.
// soft plus deadtimeout: a share disappears whenever the sharer stops, and a
// hard mount would leave Finder and shells stuck until a forced unmount.
// rsize/wsize: the macOS default transfer size is small; the server accepts
// large transfers and each transfer is a round trip over the tunnel.
func mountArgs(port string, readOnly bool, mp string) []string {
	opts := []string{
		"port=" + port,
		"mountport=" + port,
		"tcp",
		"vers=3",
		"locallocks",
		"soft",
		"deadtimeout=60",
		"rsize=1048576",
		"wsize=1048576",
	}
	if readOnly {
		opts = append(opts, "rdonly")
	}
	return []string{"mount", "-t", "nfs", "-o", strings.Join(opts, ","), "127.0.0.1:/", mp}
}

func umountArgs(mp string) [][]string {
	return [][]string{
		{"umount", mp},
		{"diskutil", "unmount", "force", mp},
	}
}

func manualUnmountHint(mp string) string {
	return "diskutil unmount force " + mp
}

func checkPrereqs() error { return nil }

// checkOwner enforces the kernel's rule for unprivileged mounts: the caller
// must own the mountpoint directory.
func checkOwner(mp string, st os.FileInfo) error {
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(sys.Uid) != os.Getuid() {
		return fmt.Errorf("mountpoint %s is not owned by you; macOS requires you to own the mountpoint", mp)
	}
	return nil
}
