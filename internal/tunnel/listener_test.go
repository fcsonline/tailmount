package tunnel

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestHandleThenAccept(t *testing.T) {
	l := NewListener(2049)
	defer l.Close()
	a, b := net.Pipe()
	defer b.Close()
	l.Handle(a)
	got, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	if got != a {
		t.Fatalf("Accept returned %v, want the handled conn", got)
	}
}

func TestAcceptBlocksUntilHandle(t *testing.T) {
	l := NewListener(2049)
	defer l.Close()
	a, b := net.Pipe()
	defer b.Close()
	res := make(chan error, 1)
	go func() {
		_, err := l.Accept()
		res <- err
	}()
	select {
	case <-res:
		t.Fatal("Accept returned before Handle")
	case <-time.After(50 * time.Millisecond):
	}
	l.Handle(a)
	if err := <-res; err != nil {
		t.Fatal(err)
	}
}

func TestCloseUnblocksAccept(t *testing.T) {
	l := NewListener(2049)
	res := make(chan error, 1)
	go func() {
		_, err := l.Accept()
		res <- err
	}()
	time.Sleep(20 * time.Millisecond)
	l.Close()
	if err := <-res; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept after Close = %v, want net.ErrClosed", err)
	}
	if _, err := l.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("second Accept after Close = %v, want net.ErrClosed", err)
	}
}

func TestHandleAfterCloseClosesConn(t *testing.T) {
	l := NewListener(2049)
	l.Close()
	l.Close() // double Close is safe
	a, b := net.Pipe()
	l.Handle(a)
	b.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := b.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("Read on peer of handled conn = %v, want EOF", err)
	}
}

func TestCloseDrainsQueued(t *testing.T) {
	l := NewListener(2049)
	a, b := net.Pipe()
	l.Handle(a)
	l.Close()
	b.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := b.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("queued conn not closed on Close: %v", err)
	}
}
