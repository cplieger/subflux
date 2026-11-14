package accounts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/access/account"
	"go.yaml.in/yaml/v4"
)

// adminSocket is the out-of-band channel every admin socket action names in
// its events.
const adminSocket = "admin-socket"

// recoveryTTL is how long a recovery link the admin socket prints stays
// valid.
const recoveryTTL = 15 * time.Minute

// ErrUnknownRole is a CreateUser role other than admin or user.
var ErrUnknownRole = errors.New("accounts: the role must be admin or user")

// ResetPassword sets username's password and, in the same transaction,
// revokes its sessions and keys and clears its sign-in locks.
func (s *Service) ResetPassword(ctx context.Context, username, password string) error {
	u, err := s.lookup(ctx, username)
	if err != nil {
		return err
	}
	return s.account.RecoverOutOfBand(ctx, account.OutOfBand(adminSocket), u.ID, access.NewSecret(password), account.RecoverOptions{})
}

// RecoveryLink mints a recovery token for username, voiding its earlier
// ones, and answers the link path /recover#<token>: the token rides in the
// fragment, which no browser sends to a server log.
func (s *Service) RecoveryLink(ctx context.Context, username string) (string, error) {
	u, err := s.lookup(ctx, username)
	if err != nil {
		return "", err
	}
	tok, err := s.account.IssueRecovery(ctx, account.OutOfBand(adminSocket), u.ID, recoveryTTL)
	if err != nil {
		return "", err
	}
	return "/recover#" + tok.Plaintext.Reveal(), nil
}

// CreateUser creates username with role admin or user (empty is user) and
// password, under the HTTP plane's username and password rules.
func (s *Service) CreateUser(ctx context.Context, username, password, role string) error {
	r, err := parseRole(role)
	if err != nil {
		return err
	}
	_, err = s.account.CreateUser(ctx, account.OutOfBand(adminSocket), account.NewUser{Username: username, Password: access.NewSecret(password), Role: r})
	return err
}

// CreateKey mints a key for username and answers its plaintext, shown once.
// No scope is the owner's full access.
func (s *Service) CreateKey(ctx context.Context, username, label string, scopes []string) (string, error) {
	u, err := s.lookup(ctx, username)
	if err != nil {
		return "", err
	}
	k, err := s.account.CreateAPIKey(ctx, account.OutOfBand(adminSocket), u.ID, account.NewKey{Label: label, Scopes: scopes})
	if err != nil {
		return "", err
	}
	return k.Plaintext.Reveal(), nil
}

// SetupToken mints a setup token, voiding earlier ones; account's
// ErrSetupComplete once a user exists.
func (s *Service) SetupToken(ctx context.Context) (string, error) {
	tok, err := s.account.IssueSetupToken(ctx, account.OutOfBand(adminSocket), setupTokenTTL)
	if err != nil {
		return "", err
	}
	return tok.Reveal(), nil
}

// lookup reads the user username names after access.NormalizeUsername, the
// bytes the store compares; a missing user is account.ErrNotFound and a
// store failure matches access.ErrAuthBackend, as account's own reads do.
func (s *Service) lookup(ctx context.Context, username string) (*access.User, error) {
	canonical, err := access.NormalizeUsername(username)
	if err != nil {
		return nil, err
	}
	u, ok, err := s.users.UserByUsername(ctx, canonical)
	switch {
	case err != nil:
		return nil, fmt.Errorf("%w: %w", access.ErrAuthBackend, err)
	case !ok:
		return nil, account.ErrNotFound
	}
	return u, nil
}

func parseRole(role string) (access.Role, error) {
	switch access.Role(role) {
	case "", access.RoleUser:
		return access.RoleUser, nil
	case access.RoleAdmin:
		return access.RoleAdmin, nil
	}
	return "", ErrUnknownRole
}

// WithPasswordLogin sets auth.password_login true in doc, a whole document
// as settings.Snapshot.Document answers it, adding the section or the key
// when absent, and answers doc. An auth value that is not a mapping is left
// for the Save to refuse at its path.
func WithPasswordLogin(doc *yaml.Node) *yaml.Node {
	on := func() *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: "true"} }
	key := func(k string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: k} }
	section := mappingValue(doc, SectionKey)
	switch {
	case section == nil:
		doc.Content = append(doc.Content, key(SectionKey), &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{key(keyPasswordLogin), on()}})
	case section.Kind != yaml.MappingNode:
	default:
		for i := 0; i+1 < len(section.Content); i += 2 {
			if section.Content[i].Value == keyPasswordLogin {
				section.Content[i+1] = on()
				return doc
			}
		}
		section.Content = append(section.Content, key(keyPasswordLogin), on())
	}
	return doc
}

// mappingValue is the value under key in the mapping m, nil when absent.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}
