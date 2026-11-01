package app

import (
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
)

// pipeClosed reports whether c's end of a net.Pipe was closed.
func pipeClosed(c net.Conn) bool {
	_, err := c.Write([]byte{0})
	return errors.Is(err, io.ErrClosedPipe)
}

func TestNewConnsDrain(t *testing.T) {
	pipe := func(t *testing.T) net.Conn {
		t.Helper()
		c, peer := net.Pipe()
		t.Cleanup(func() { _ = c.Close(); _ = peer.Close() })
		go func() { _, _ = io.Copy(io.Discard, peer) }()
		return c
	}

	t.Run("tracked_before_drain", func(t *testing.T) {
		n := &newConns{m: map[net.Conn]struct{}{}}
		c := pipe(t)
		n.track(c, http.StateNew)
		n.closeAll()
		if !pipeClosed(c) {
			t.Error("closeAll left a StateNew connection open")
		}
	})

	t.Run("arriving_during_drain", func(t *testing.T) {
		n := &newConns{m: map[net.Conn]struct{}{}}
		n.closeAll()
		c := pipe(t)
		n.track(c, http.StateNew)
		if !pipeClosed(c) {
			t.Error("a StateNew connection after closeAll stayed open")
		}
		if len(n.m) != 0 {
			t.Errorf("tracked %d connections after closeAll, want 0", len(n.m))
		}
	})

	t.Run("sent_a_request", func(t *testing.T) {
		n := &newConns{m: map[net.Conn]struct{}{}}
		c := pipe(t)
		n.track(c, http.StateNew)
		n.track(c, http.StateActive)
		n.closeAll()
		if pipeClosed(c) {
			t.Error("closeAll closed a connection serving a request")
		}
	})
}
