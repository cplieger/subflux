package app

import (
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// acceptLog closes second when Accept is entered for the second time:
// http.Server marks the first connection StateNew before that call.
type acceptLog struct {
	net.Listener
	second chan struct{}
	n      atomic.Int32
}

func (a *acceptLog) Accept() (net.Conn, error) {
	if a.n.Add(1) == 2 {
		close(a.second)
	}
	return a.Listener.Accept()
}

func TestShutdownClosesSilentConnection(t *testing.T) {
	shorten(t, &httpGrace, time.Second)
	shorten(t, &drainBudget, 2*time.Second)
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	al := &acceptLog{Listener: ln, second: make(chan struct{})}
	b := boot(t, "", func(c *Config) { c.Listener = al })
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", b.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	select {
	case <-al.second:
	case <-time.After(waitBudget):
		t.Fatal("the server never accepted the connection")
	}
	started := time.Now()
	b.cancel()
	if err := b.wait(t); err != nil {
		t.Errorf("Run = %v with a connection that sent no request", err)
	}
	if took := time.Since(started); took > 500*time.Millisecond {
		t.Errorf("the drain waited %v on a connection that sent no request", took)
	}
	if err := conn.SetReadDeadline(time.Now().Add(waitBudget)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Errorf("read on the silent connection after the drain = %v, want EOF", err)
	}
}
