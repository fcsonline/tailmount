// Package share publishes a directory as an NFSv3 export behind a tailcat
// address.
package share

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	billy "github.com/go-git/go-billy/v5"
	"github.com/tailscale/tailcat"
	nfs "github.com/willscott/go-nfs"
	nfshelper "github.com/willscott/go-nfs/helpers"
	"tailscale.com/wgengine/filter"

	"github.com/fcsonline/tailmount/internal/rofs"
	"github.com/fcsonline/tailmount/internal/sharefs"
	"github.com/fcsonline/tailmount/internal/tunnel"
	"github.com/fcsonline/tailmount/internal/version"
)

// handleLimit bounds the server-side cache that maps NFS file handles to
// paths. A handle evicted while a client still holds it makes that client
// see ESTALE, and kernels cache handles for every file they have touched,
// so the limit must comfortably exceed the number of files a client walks.
const handleLimit = 1_000_000

// drainTimeout bounds how long shutdown waits for in-flight NFS writes.
const drainTimeout = 5 * time.Second

// Options configure a share.
type Options struct {
	// Dir is the directory to publish. Empty means the working directory.
	Dir string
	// ReadOnly enforces read-only access on the server.
	ReadOnly bool
	// Verbose enables tailcat and go-nfs diagnostic logging.
	Verbose bool
	// Out receives the address and instructions for humans. Defaults to stdout.
	Out io.Writer
}

// Run publishes the directory until ctx is cancelled, then shuts down.
func Run(ctx context.Context, o Options) error {
	if o.Out == nil {
		o.Out = os.Stdout
	}
	dir, err := resolveDir(o.Dir)
	if err != nil {
		return err
	}
	name := filepath.Base(dir)
	mode := ModeReadWrite
	if o.ReadOnly {
		mode = ModeReadOnly
	}

	var fs billy.Filesystem = sharefs.New(dir)
	if o.ReadOnly {
		fs = rofs.New(fs)
	}
	handler := nfshelper.NewCachingHandler(nfshelper.NewNullAuthHandler(fs), handleLimit)

	if o.Verbose {
		nfs.Log.SetLevel(nfs.DebugLevel)
		tailcat.Verbose = true
	} else {
		nfs.Log.SetLevel(nfs.WarnLevel)
	}

	nfsL := tunnel.NewListener(PortNFS)
	serveErr := make(chan error, 1)
	go func() { serveErr <- nfs.Serve(nfsL, handler) }()

	info := Info{Mode: mode, Name: name, Version: version.Version}
	srv := &tailcat.Server{
		Logf:           logf(o.Verbose),
		ServedTCPPorts: []filter.PortRange{{First: PortNFS, Last: PortInfo}},
		OnTCP: func(port uint16) func(net.Conn) {
			switch port {
			case PortNFS:
				return func(c net.Conn) {
					log.Printf("client opened an NFS session")
					nfsL.Handle(c)
				}
			case PortInfo:
				return func(c net.Conn) {
					defer c.Close()
					c.SetWriteDeadline(time.Now().Add(5 * time.Second))
					if err := json.NewEncoder(c).Encode(info); err != nil {
						log.Printf("info request failed: %v", err)
					}
				}
			default:
				return nil
			}
		},
	}
	if err := srv.Start(); err != nil {
		nfsL.Close()
		return fmt.Errorf("start tailcat server: %w", err)
	}

	addr := srv.TailcatAddr()
	fmt.Fprintf(o.Out, "sharing %s (%s)\n", dir, mode)
	fmt.Fprintf(o.Out, "# 🐈 tailmount address: %s\n", addr)
	fmt.Fprintf(o.Out, "on another machine run:\n  tailmount %s\n", addr)
	fmt.Fprintf(o.Out, "(mounts at ~/tailmount/%s; pass a directory to choose another place)\n", name)
	fmt.Fprintf(o.Out, "press Ctrl-C to stop sharing\n")

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		srv.Close()
		return fmt.Errorf("nfs server stopped: %w", err)
	}

	log.Printf("shutting down")
	nfsL.Close()
	drainCtx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	if err := srv.DrainTCP(drainCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		log.Printf("drain: %v", err)
	}
	return srv.Close()
}

func resolveDir(dir string) (string, error) {
	var err error
	if dir == "" {
		dir, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("%s is not a directory", dir)
	}
	return dir, nil
}

// logf returns the tailcat log sink. tailcat logs every internal state
// change through Logf; by default only the client handshake lines are kept,
// rewritten as a short human message.
func logf(verbose bool) func(format string, args ...any) {
	if verbose {
		return log.Printf
	}
	return func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		switch {
		case strings.Contains(msg, "got meow from"):
			log.Printf("client connected")
		case strings.Contains(msg, "not in allowedClients"):
			log.Printf("rejected a client that is not allowed")
		}
	}
}
