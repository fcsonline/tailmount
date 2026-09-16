// Package tunnel adapts tailcat's per-connection callback model to the
// net.Listener interface expected by servers such as go-nfs.
package tunnel

import (
	"net"
	"sync"
)

// Listener is a net.Listener fed by Handle instead of by a socket.
//
// tailcat delivers each incoming TCP connection to an OnTCP handler; go-nfs
// wants to Accept from a net.Listener. Listener bridges the two.
type Listener struct {
	port  uint16
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

// NewListener returns a Listener that reports the given tunnel port in Addr.
func NewListener(port uint16) *Listener {
	return &Listener{
		port:  port,
		conns: make(chan net.Conn, 16),
		done:  make(chan struct{}),
	}
}

// Handle queues c for the next Accept. It is safe to use as a tailcat OnTCP
// handler. After Close, Handle closes c instead of queueing it.
func (l *Listener) Handle(c net.Conn) {
	select {
	case <-l.done:
		c.Close()
	case l.conns <- c:
	}
}

// Accept returns the next connection passed to Handle. After Close it
// returns net.ErrClosed, which has no Temporary method, so go-nfs stops
// serving instead of retrying.
func (l *Listener) Accept() (net.Conn, error) {
	select {
	case <-l.done:
		return nil, net.ErrClosed
	default:
	}
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

// Close stops the listener and closes any connection that was queued but
// never accepted. Calling Close more than once is safe.
func (l *Listener) Close() error {
	l.once.Do(func() {
		close(l.done)
		for {
			select {
			case c := <-l.conns:
				c.Close()
			default:
				return
			}
		}
	})
	return nil
}

// Addr reports the tunnel port the listener represents.
func (l *Listener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv6unspecified, Port: int(l.port)}
}
