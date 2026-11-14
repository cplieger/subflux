package app

import (
	"context"

	"github.com/cplieger/subflux/internal/accounts"
	"github.com/cplieger/subflux/internal/adminsock"
	"github.com/cplieger/subflux/internal/settings"
)

// passwordLogin is the admin socket's enable-password-login: a Save of the
// file as it stands with auth.password_login set true.
type passwordLogin struct{ svc *settings.Service }

// EnablePasswordLogin writes nothing with no file, where password sign-in
// is on, or when the file is not one mapping (a refused unconfigured boot
// included), answering the load's field errors; otherwise it answers the
// Save's.
func (p passwordLogin) EnablePasswordLogin(ctx context.Context) (adminsock.PasswordLogin, error) {
	snap := p.svc.Snapshot()
	if snap.Mode() == settings.Unconfigured {
		return adminsock.PasswordLogin{AlreadyOn: true}, nil
	}
	doc := snap.Document()
	if doc == nil {
		fields, _ := snap.Failure()
		return adminsock.PasswordLogin{Fields: fields}, nil
	}
	_, fields, err := p.svc.Save(ctx, accounts.WithPasswordLogin(doc), snap.Revision())
	return adminsock.PasswordLogin{Fields: fields}, err
}
