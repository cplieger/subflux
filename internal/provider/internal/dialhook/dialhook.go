// Package dialhook carries a TLS dial on a request context, so a test can
// serve a provider's real hosts in memory while the provider Client still
// runs its host pin first. It holds no package state, so parallel tests each
// carry their own hook.
package dialhook

import (
	"context"
	"net"
)

// DialTLS returns a connection whose TLS handshake has completed for addr's
// host.
type DialTLS func(ctx context.Context, network, addr string) (net.Conn, error)

type key struct{}

// With returns ctx carrying d.
func With(ctx context.Context, d DialTLS) context.Context {
	return context.WithValue(ctx, key{}, d)
}

// From returns the DialTLS ctx carries, or nil.
func From(ctx context.Context) DialTLS {
	d, _ := ctx.Value(key{}).(DialTLS)
	return d
}
