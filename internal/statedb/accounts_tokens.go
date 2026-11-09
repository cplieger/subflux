package statedb

import (
	"context"
	"slices"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/bolttable"
)

func tokenOf(hash string, r *tokenRow) access.OneTimeToken {
	return access.OneTimeToken{
		CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, Hash: access.NewHash(hash), Purpose: access.TokenPurpose(r.Purpose),
		Role: access.Role(r.Role), Subject: r.Subject, IssuedByKey: r.IssuedByKey,
	}
}

func (r authReader) token(hash access.Hash) (access.OneTimeToken, bool, error) {
	row, ok, err := r.a.tokens.Get(r.r, hash.Reveal())
	if err != nil || !ok {
		return access.OneTimeToken{}, false, err
	}
	return tokenOf(hash.Reveal(), &row), true, nil
}

// liveTokens lists, in hash order, the tokens of a listed purpose whose
// ExpiresAt is after now (rule 7).
func (r authReader) liveTokens(purposes []access.TokenPurpose, now time.Time) ([]access.OneTimeToken, error) {
	out := []access.OneTimeToken{}
	err := r.a.tokens.Scan(r.r, bolttable.Range{}, func(hash string, row *tokenRow) (bool, error) {
		if slices.Contains(purposes, access.TokenPurpose(row.Purpose)) && row.ExpiresAt.After(now) {
			out = append(out, tokenOf(hash, row))
		}
		return true, nil
	})
	return out, err
}

// Tokens are silent, so every token change sets the accounts mark itself:
// invites and a key's issued links are listed beside users and keys.

// CreateOneTimeToken stores tok, refusing a stored token of the same hash.
func (x *accountTx) CreateOneTimeToken(_ context.Context, tok access.OneTimeToken) error { //nolint:gocritic // hugeParam: account.TokenStore's signature
	if tok.Hash.IsZero() {
		return errNoHash
	}
	hash := tok.Hash.Reveal()
	if _, ok, err := x.s.a.tokens.Get(x.tx.Read(), hash); err != nil || ok {
		return existsErr(ok, err)
	}
	row := tokenRow{
		CreatedAt: tok.CreatedAt, ExpiresAt: tok.ExpiresAt, Purpose: string(tok.Purpose), Role: string(tok.Role),
		Subject: tok.Subject, IssuedByKey: tok.IssuedByKey,
	}
	if err := x.s.a.tokens.Put(x.tx, hash, &row); err != nil {
		return err
	}
	x.tx.Mark(MarkAccounts)
	return nil
}

func (x *accountTx) OneTimeTokenByHash(_ context.Context, hash access.Hash) (access.OneTimeToken, bool, error) {
	return x.read().token(hash)
}

// ConsumeOneTimeToken deletes hash's token, expired or not, and reports it
// found only when its ExpiresAt is after now (rule 7).
func (x *accountTx) ConsumeOneTimeToken(_ context.Context, hash access.Hash, now time.Time) (access.OneTimeToken, bool, error) {
	tok, ok, err := x.read().token(hash)
	if err != nil || !ok {
		return access.OneTimeToken{}, false, err
	}
	if _, err := x.s.a.tokens.Delete(x.tx, hash.Reveal()); err != nil {
		return access.OneTimeToken{}, false, err
	}
	x.tx.Mark(MarkAccounts)
	if !tok.ExpiresAt.After(now) {
		return access.OneTimeToken{}, false, nil
	}
	return tok, true, nil
}

func (x *accountTx) DeleteOneTimeTokens(_ context.Context, purpose access.TokenPurpose, subject int64) (int, error) {
	return x.deleteTokensIn(x.s.a.tokensBySub, bolttable.Exact(appendText(idKeys.AppendKey(nil, subject), string(purpose))))
}

func (x *accountTx) LiveOneTimeTokens(_ context.Context, purposes []access.TokenPurpose, now time.Time) ([]access.OneTimeToken, error) {
	return x.read().liveTokens(purposes, now)
}

// DeleteOneTimeTokensIssuedBy deletes exactly the tokens a listed key
// issued, through the sparse token_issued_by index, so 0 matches nothing.
func (x *accountTx) DeleteOneTimeTokensIssuedBy(_ context.Context, keyIDs []int64) (int, error) {
	n := 0
	for _, id := range keyIDs {
		if id == 0 {
			continue
		}
		d, err := x.deleteTokensIn(x.s.a.tokensByIssuer, bolttable.Exact(idKeys.AppendKey(nil, id)))
		if err != nil {
			return 0, err
		}
		n += d
	}
	return n, nil
}

// deleteTokensIn deletes every token holding an entry of ix in rg and
// returns how many it deleted.
func (x *accountTx) deleteTokensIn(ix *bolttable.Index[string, tokenRow], rg bolttable.Range) (int, error) { //nolint:gocritic // hugeParam: bolttable's Scan takes a Range by value
	var hashes []string
	if err := ix.Scan(x.tx.Read(), rg, func(_ []byte, hash string, _ []byte) (bool, error) {
		hashes = append(hashes, hash)
		return true, nil
	}); err != nil {
		return 0, err
	}
	for _, h := range hashes {
		if _, err := x.s.a.tokens.Delete(x.tx, h); err != nil {
			return 0, err
		}
	}
	if len(hashes) > 0 {
		x.tx.Mark(MarkAccounts)
	}
	return len(hashes), nil
}

// CreateOneTimeToken stores tok, refusing a stored token of its hash.
func (a *AccountStore) CreateOneTimeToken(ctx context.Context, tok access.OneTimeToken) error { //nolint:gocritic // hugeParam: account.TokenStore's signature
	return a.single(ctx, "create token", func(x *accountTx) error { return x.CreateOneTimeToken(ctx, tok) })
}

// OneTimeTokenByHash reads the token stored under hash.
func (a *AccountStore) OneTimeTokenByHash(ctx context.Context, hash access.Hash) (access.OneTimeToken, bool, error) {
	return viewFound(ctx, a, func(r authReader) (access.OneTimeToken, bool, error) { return r.token(hash) })
}

// ConsumeOneTimeToken deletes hash's token and answers it when live at now (rule 7).
func (a *AccountStore) ConsumeOneTimeToken(ctx context.Context, hash access.Hash, now time.Time) (access.OneTimeToken, bool, error) {
	type consumed struct {
		tok   access.OneTimeToken
		found bool
	}
	c, err := singleValue(ctx, a, "consume token", func(x *accountTx) (c consumed, err error) {
		c.tok, c.found, err = x.ConsumeOneTimeToken(ctx, hash, now)
		return c, err
	})
	return c.tok, c.found, err
}

// DeleteOneTimeTokens deletes subject's tokens of purpose and returns how many.
func (a *AccountStore) DeleteOneTimeTokens(ctx context.Context, purpose access.TokenPurpose, subject int64) (int, error) {
	return singleValue(ctx, a, "delete tokens", func(x *accountTx) (int, error) { return x.DeleteOneTimeTokens(ctx, purpose, subject) })
}

// LiveOneTimeTokens lists, in one View, every token of a listed purpose
// whose ExpiresAt is after now.
func (a *AccountStore) LiveOneTimeTokens(ctx context.Context, purposes []access.TokenPurpose, now time.Time) ([]access.OneTimeToken, error) {
	return viewList(ctx, a, func(r authReader) ([]access.OneTimeToken, error) { return r.liveTokens(purposes, now) })
}

// DeleteOneTimeTokensIssuedBy deletes the tokens a listed key issued and returns how many.
func (a *AccountStore) DeleteOneTimeTokensIssuedBy(ctx context.Context, keyIDs []int64) (int, error) {
	return singleValue(ctx, a, "delete issued tokens", func(x *accountTx) (int, error) { return x.DeleteOneTimeTokensIssuedBy(ctx, keyIDs) })
}
