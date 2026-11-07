// Package providertest serves provider fakes on their real host names, in
// memory and over TLS, for tests of code that sends requests through a
// provider Client. Only _test.go files import it (TestImportConfinement).
package providertest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/provider/internal/dialhook"
)

// Serve serves h for hosts and returns ctx carrying the TLS dial that reaches
// it. A request from the returned context to a host Serve did not list goes to
// ctx's earlier dial, so two Serve calls compose, or is refused. Serve called
// inside a synctest bubble keeps every goroutine and connection in it.
func Serve(t testing.TB, ctx context.Context, h http.Handler, hosts ...string) context.Context { //nolint:revive // context-as-argument: a test helper takes t first
	t.Helper()
	if len(hosts) == 0 {
		t.Fatal("providertest.Serve: no hosts")
	}
	cert, pool := certificate(t, hosts)
	l := newPipeListener()
	srv := &http.Server{
		Handler:           h,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: time.Minute,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.ServeTLS(l, "", "")
	}()
	t.Cleanup(func() {
		_ = srv.Close()
		<-done
	})

	prev := dialhook.From(ctx)
	return dialhook.With(ctx, func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(hosts, host) {
			if prev != nil {
				return prev(ctx, network, addr)
			}
			return nil, fmt.Errorf("providertest: no server for host %q", host)
		}
		c, err := l.dial(ctx)
		if err != nil {
			return nil, err
		}
		tc := tls.Client(c, &tls.Config{RootCAs: pool, ServerName: host, NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12})
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = c.Close()
			return nil, err
		}
		return tc, nil
	})
}

// Unresolvable returns ctx with every dial to hosts failing as a resolver
// answering no such host does; other hosts go to ctx's earlier dial.
func Unresolvable(ctx context.Context, hosts ...string) context.Context {
	prev := dialhook.From(ctx)
	return dialhook.With(ctx, func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if slices.Contains(hosts, host) {
			return nil, &net.OpError{Op: "dial", Net: network, Err: &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}}
		}
		if prev != nil {
			return prev(ctx, network, addr)
		}
		return nil, fmt.Errorf("providertest: no server for host %q", host)
	})
}

// certificate makes a self-signed ECDSA certificate for hosts. Its validity
// spans 1990 to 2100 because a synctest bubble's clock starts in 2000.
func certificate(t testing.TB, hosts []string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: hosts[0]},
		DNSNames:              hosts,
		NotBefore:             time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

var errListenerClosed = errors.New("providertest: listener closed")

// pipeListener hands the server one end of a net.Pipe per dial.
type pipeListener struct {
	conns     chan net.Conn
	closed    chan struct{}
	closeOnce sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) dial(ctx context.Context) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.closed:
	case <-ctx.Done():
	}
	_ = client.Close()
	_ = server.Close()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, errListenerClosed
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (*pipeListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "providertest" }
