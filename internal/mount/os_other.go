//go:build !darwin && !linux

package mount

import (
	"context"
	"errors"
	"io"
)

// ErrBadAddr reports an address that does not parse as a tailcat address.
var ErrBadAddr = errors.New("invalid tailmount address")

var errUnsupported = errors.New("mounting is only supported on macOS and Linux")

// Options configure a mount.
type Options struct {
	Addr       string
	Mountpoint string
	Verbose    bool
	Out        io.Writer
}

// Run is not supported on this platform.
func Run(context.Context, Options) error { return errUnsupported }

// Unmount is not supported on this platform.
func Unmount(string) error { return errUnsupported }
