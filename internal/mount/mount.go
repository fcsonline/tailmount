//go:build darwin || linux

// Package mount connects to a tailmount share and mounts it with the
// operating system's NFS client.
package mount

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/tailscale/tailcat"

	"github.com/fcsonline/tailmount/internal/share"
)

// ErrBadAddr reports an address that does not parse as a tailcat address.
var ErrBadAddr = errors.New("invalid tailmount address")

const (
	pingTimeout  = 15 * time.Second
	infoTimeout  = 5 * time.Second
	mountTimeout = 60 * time.Second
)

// Options configure a mount.
type Options struct {
	// Addr is the tailmount address printed by the sharer.
	Addr string
	// Mountpoint is the empty directory to mount on. Required.
	Mountpoint string
	// Verbose enables tailcat diagnostic logging.
	Verbose bool
	// Out receives status lines for humans. Defaults to stdout.
	Out io.Writer
}

// Run mounts the share, waits until ctx is cancelled or the sharer goes
// away, then unmounts and cleans up.
func Run(ctx context.Context, o Options) error {
	if o.Out == nil {
		o.Out = os.Stdout
	}
	addr := tailcat.Addr(o.Addr)
	if _, err := tailcat.ParseAddr(addr); err != nil {
		return fmt.Errorf("%w: %v", ErrBadAddr, err)
	}
	if err := checkPrereqs(); err != nil {
		return err
	}

	cl := tailcat.NewClient(addr)
	if o.Verbose {
		cl.Logf = log.Printf
		tailcat.Verbose = true
	} else {
		cl.Logf = func(string, ...any) {}
	}
	defer cl.Close()

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	_, err := cl.Ping(pingCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("cannot reach share: %w (is the sharer still running tailmount?)", err)
	}

	info, err := fetchInfo(ctx, cl)
	if err != nil {
		return fmt.Errorf("share did not answer info request: %w", err)
	}
	readOnly := info.Mode == share.ModeReadOnly

	mp, created, err := prepareMountpoint(o.Mountpoint)
	if err != nil {
		return err
	}
	removeIfCreated := func() {
		if created {
			os.Remove(mp)
		}
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		removeIfCreated()
		return err
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	var mounted bool
	var mountedMu sync.Mutex
	serverGone := make(chan struct{})
	var goneOnce sync.Once
	go proxy(ctx, ln, cl, func(err error) {
		mountedMu.Lock()
		m := mounted
		mountedMu.Unlock()
		log.Printf("tunnel dial failed: %v", err)
		if m {
			goneOnce.Do(func() { close(serverGone) })
		}
	})

	if err := runMount(ctx, port, readOnly, mp); err != nil {
		removeIfCreated()
		return err
	}
	mountedMu.Lock()
	mounted = true
	mountedMu.Unlock()

	fmt.Fprintf(o.Out, "mounted %s (%s) at %s\n", info.Name, info.Mode, mp)
	fmt.Fprintf(o.Out, "press Ctrl-C to unmount\n")

	select {
	case <-ctx.Done():
	case <-serverGone:
		fmt.Fprintf(o.Out, "the sharer went away, unmounting\n")
	}

	if err := Unmount(mp); err != nil {
		return err
	}
	removeIfCreated()
	return nil
}

// Unmount detaches mp, trying the platform's forceful variant if the plain
// one fails.
func Unmount(mp string) error {
	mp, err := filepath.Abs(mp)
	if err != nil {
		return err
	}
	var lastErr error
	for i, argv := range umountArgs(mp) {
		if i > 0 {
			time.Sleep(2 * time.Second)
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			lastErr = err
			continue
		}
		if !isMountpoint(mp) {
			return nil
		}
		lastErr = errors.New("still mounted after umount")
	}
	return fmt.Errorf("could not unmount %s: %w\nunmount it by hand with: %s", mp, lastErr, manualUnmountHint(mp))
}

func fetchInfo(ctx context.Context, cl *tailcat.Client) (share.Info, error) {
	var info share.Info
	ctx, cancel := context.WithTimeout(ctx, infoTimeout)
	defer cancel()
	c, err := cl.DialTCPPort(ctx, share.PortInfo)
	if err != nil {
		return info, err
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(infoTimeout))
	if err := json.NewDecoder(c).Decode(&info); err != nil {
		return info, err
	}
	if info.Name == "" {
		return info, errors.New("empty share name")
	}
	return info, nil
}

// proxy accepts local connections and pipes each into the tunnel's NFS port.
// onDialError is called for every failed tunnel dial.
func proxy(ctx context.Context, ln net.Listener, cl *tailcat.Client, onDialError func(error)) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			t, err := cl.DialTCPPort(ctx, share.PortNFS)
			if err != nil {
				c.Close()
				if ctx.Err() == nil {
					onDialError(err)
				}
				return
			}
			tailcat.ProxyConns(c, t)
		}()
	}
}

// prepareMountpoint returns the absolute mountpoint and whether this call
// created it. An existing directory must be empty, not already a mount, and
// owned by the current user where the platform requires that.
func prepareMountpoint(mp string) (string, bool, error) {
	if mp == "" {
		return "", false, errors.New("a mountpoint directory is required")
	}
	mp, err := filepath.Abs(mp)
	if err != nil {
		return "", false, err
	}
	st, err := os.Stat(mp)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(mp, 0o755); err != nil {
			return "", false, err
		}
		return mp, true, nil
	case err != nil:
		return "", false, err
	}
	if !st.IsDir() {
		return "", false, fmt.Errorf("mountpoint %s is not a directory", mp)
	}
	if isMountpoint(mp) {
		return "", false, fmt.Errorf("mountpoint %s already has something mounted on it", mp)
	}
	entries, err := os.ReadDir(mp)
	if err != nil {
		return "", false, err
	}
	if len(entries) > 0 {
		return "", false, fmt.Errorf("mountpoint %s is not empty (pass an empty directory)", mp)
	}
	if err := checkOwner(mp, st); err != nil {
		return "", false, err
	}
	return mp, false, nil
}

// isMountpoint reports whether p is the root of a mounted filesystem, by
// comparing its device with its parent's.
func isMountpoint(p string) bool {
	st, err := os.Stat(p)
	if err != nil {
		return false
	}
	parent, err := os.Stat(filepath.Dir(p))
	if err != nil {
		return false
	}
	a, ok1 := st.Sys().(*syscall.Stat_t)
	b, ok2 := parent.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 {
		return false
	}
	return a.Dev != b.Dev
}

func runMount(ctx context.Context, port int, readOnly bool, mp string) error {
	ctx, cancel := context.WithTimeout(ctx, mountTimeout)
	defer cancel()
	argv := mountArgs(strconv.Itoa(port), readOnly, mp)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mount failed: %w", err)
	}
	return nil
}
