// Command tailmount shares the current directory as a volume that anyone
// holding its address can mount, over a tailcat tunnel.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/tailscale/tailcat"

	"github.com/fcsonline/tailmount/internal/mount"
	"github.com/fcsonline/tailmount/internal/share"
	"github.com/fcsonline/tailmount/internal/version"
)

const usage = `tailmount shares a directory as a mountable volume over an encrypted tailcat tunnel.

Usage:
  tailmount                            share the current directory (read-write)
  tailmount [--ro] [-v] share [dir]    share a directory
  tailmount [-v] mount <addr> [dir]    mount a share (dir defaults to ~/tailmount/<name>)
  tailmount <addr> [dir]               same as "mount"
  tailmount unmount <dir>              unmount a share mounted earlier
  tailmount version                    print the version

Flags:
  --ro   share read-only (share only)
  -v     verbose tunnel and NFS logging

The address printed by the sharer is the only secret. Anyone who has it can
mount the share while the sharer is running.
`

func main() {
	log.SetFlags(log.Ltime)
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(args) == 0 {
		return exit(share.Run(ctx, share.Options{}))
	}
	switch args[0] {
	case "version", "--version", "-version":
		fmt.Println("tailmount", version.Version)
		return 0
	case "help", "--help", "-help", "-h":
		fmt.Print(usage)
		return 0
	}
	if isAddr(args[0]) {
		args = append([]string{"mount"}, args...)
	} else if strings.HasPrefix(args[0], "-") {
		args = hoistCommand(args)
	}

	switch args[0] {
	case "share":
		fs := flag.NewFlagSet("share", flag.ContinueOnError)
		ro := fs.Bool("ro", false, "share read-only")
		verbose := fs.Bool("v", false, "verbose logging")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if fs.NArg() > 1 {
			fmt.Fprintln(os.Stderr, "share takes at most one directory")
			return 2
		}
		return exit(share.Run(ctx, share.Options{Dir: fs.Arg(0), ReadOnly: *ro, Verbose: *verbose}))
	case "mount":
		fs := flag.NewFlagSet("mount", flag.ContinueOnError)
		verbose := fs.Bool("v", false, "verbose logging")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if fs.NArg() < 1 || fs.NArg() > 2 {
			fmt.Fprintln(os.Stderr, "usage: tailmount mount <addr> [dir]")
			return 2
		}
		return exit(mount.Run(ctx, mount.Options{Addr: fs.Arg(0), Mountpoint: fs.Arg(1), Verbose: *verbose}))
	case "unmount", "umount":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: tailmount unmount <dir>")
			return 2
		}
		return exit(mount.Unmount(args[1]))
	default:
		if strings.HasPrefix(args[0], "tc") && len(args[0]) > 16 {
			// Most likely a mistyped or truncated address.
			return exit(mount.Run(ctx, mount.Options{Addr: args[0]}))
		}
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// hoistCommand accepts flags before the command name, as in
// "tailmount --ro share dir", by moving the command to the front. Flags with
// no command default to "share". All flags are booleans, so a non-flag
// argument is always the command or a positional argument.
func hoistCommand(args []string) []string {
	for i, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		switch a {
		case "share", "mount", "unmount", "umount":
			out := []string{a}
			out = append(out, args[:i]...)
			return append(out, args[i+1:]...)
		}
		break
	}
	return append([]string{"share"}, args...)
}

func isAddr(s string) bool {
	if !strings.HasPrefix(s, "tc") {
		return false
	}
	_, err := tailcat.ParseAddr(tailcat.Addr(s))
	return err == nil
}

func exit(err error) int {
	if err == nil {
		return 0
	}
	fmt.Fprintln(os.Stderr, "tailmount:", err)
	if errors.Is(err, mount.ErrBadAddr) {
		return 2
	}
	return 1
}
