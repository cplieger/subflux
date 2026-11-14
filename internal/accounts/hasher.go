package accounts

import (
	"context"

	"github.com/cplieger/access"
)

// Hasher is the provider funnel's credential hasher over the account
// service's Argon2id hasher, so both share one hash budget: together they
// hold at most four derivations, and a busy budget answers
// access.ErrHashBusy.
type Hasher struct{ h *access.Hasher }

// Hash answers credential's encoded Argon2id hash.
func (h *Hasher) Hash(ctx context.Context, credential string) (string, error) {
	encoded, err := h.h.Hash(ctx, credential)
	if err != nil {
		return "", err
	}
	return encoded.Reveal(), nil
}

// Verify reports whether credential matches fingerprint, a Hash answer.
func (h *Hasher) Verify(ctx context.Context, fingerprint, credential string) (bool, error) {
	ok, _, err := h.h.Verify(ctx, credential, access.NewHash(fingerprint))
	return ok, err
}
