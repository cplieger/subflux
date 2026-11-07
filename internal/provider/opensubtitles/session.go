package opensubtitles

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/cplieger/subflux/internal/provider"
)

// tokenLife is how long a login's token is used before the next login.
const tokenLife = 12 * time.Hour

// session is one login: its token and the host searches go to.
type session struct {
	at    time.Time
	token string
	host  string
}

// session answers the held login, logging in when there is none or it
// expired. The lock is held across the login, so concurrent calls share one.
func (a *adapter) session(ctx context.Context) (session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sess.token != "" && time.Since(a.sess.at) < tokenLife {
		return a.sess, nil
	}
	s, err := a.login(ctx)
	if err != nil {
		return session{}, err
	}
	a.sess = s
	return s, nil
}

// forget drops the held login when it is still token's.
func (a *adapter) forget(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sess.token == token {
		a.sess = session{}
	}
}

type loginAnswer struct {
	Token   string `json:"token"`
	BaseURL string `json:"base_url"`
	User    struct {
		VIP bool `json:"vip"`
	} `json:"user"`
}

// login POSTs the account to /login. Its base_url, the VIP search host, is
// used only when it is the listed one, so a forged answer cannot send the
// token elsewhere; the account's rate follows its VIP flag.
func (a *adapter) login(ctx context.Context) (session, error) {
	body, err := json.Marshal(map[string]string{keyUsername: a.user, keyPassword: a.password})
	if err != nil {
		return session{}, err
	}
	var r loginAnswer
	if err := a.call(ctx, http.MethodPost, apiHost, "/login", nil, body, "", maxAnswerBody, &r); err != nil {
		return session{}, err
	}
	if r.Token == "" {
		return session{}, provider.Fail(provider.Permanent, "OpenSubtitles answered a login with no token")
	}
	host := apiHost
	if r.BaseURL == vipHost {
		host = vipHost
	}
	if r.User.VIP {
		a.pace.SetLimit(vipRate)
	} else {
		a.pace.SetLimit(freeRate)
	}
	return session{at: time.Now(), token: r.Token, host: host}, nil
}
