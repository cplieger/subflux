package adminsock

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/access/account"
	"github.com/cplieger/go/httpserver"
	"github.com/cplieger/go/textsafe"
	"github.com/cplieger/subflux/internal/accounts"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/statedb"
)

// The socket's read bounds, the HTTP port's; it has no write timeout, since
// store --verify runs as long as the store is large.
const (
	maxBody           = 4 << 10
	readTimeout       = 10 * time.Second
	readHeaderTimeout = 10 * time.Second
	maxHeaderBytes    = 64 << 10
)

type accountActions interface {
	ResetPassword(ctx context.Context, username, password string) error
	RecoveryLink(ctx context.Context, username string) (string, error)
	CreateUser(ctx context.Context, username, password, role string) error
	CreateKey(ctx context.Context, username, label string, scopes []string) (string, error)
	SetupToken(ctx context.Context) (string, error)
}

// PasswordLogin is EnablePasswordLogin's outcome: Fields when it was
// refused and nothing was written, AlreadyOn when there is no file.
type PasswordLogin struct {
	Fields    []formschema.FieldError
	AlreadyOn bool
}

type passwordLogin interface {
	EnablePasswordLogin(ctx context.Context) (PasswordLogin, error)
}

type inspector interface {
	Inspect(ctx context.Context, o statedb.InspectOptions) (statedb.Inspection, error)
}

var (
	_ accountActions = (*accounts.Service)(nil)
	_ inspector      = (*statedb.Store)(nil)
)

// Deps is what the socket's commands run through. Every field but Logger
// is required; a nil Logger is slog.Default().
type Deps struct {
	Accounts accountActions
	Settings passwordLogin
	Store    inspector
	Logger   *slog.Logger
}

// Server answers the admin socket's one route.
type Server struct {
	deps Deps
}

// New returns a Server over d.
func New(d Deps) *Server {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Server{deps: d}
}

// Serve answers on ln until ctx ends, then shuts down within httpserver's
// default grace, waiting for the requests in flight; it returns a serve
// error at once.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := httpserver.New(s.handler())
	srv.ReadTimeout = readTimeout
	srv.ReadHeaderTimeout = readHeaderTimeout
	srv.MaxHeaderBytes = maxHeaderBytes
	srv.ErrorLog = slog.NewLogLogger(s.deps.Logger.Handler(), slog.LevelWarn)
	return httpserver.Run(ctx, srv, ln)
}

// handler serves POST / with one JSON Request, answering a Reply or an
// Error.
func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /{$}", s.handle)
	return httpserver.Recoverer(httpserver.WithRecoverLogger(s.deps.Logger))(mux)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	req, err := decode(w, r)
	if err != nil {
		s.fail(w, r, &req, err)
		return
	}
	reply, err := s.run(r.Context(), &req)
	if err != nil {
		s.fail(w, r, &req, err)
		return
	}
	s.deps.Logger.Info("adminsock: command", "command", req.Command.String(), "username", textsafe.SanitizeSingleLine(req.Username))
	writeJSON(w, http.StatusOK, reply)
}

// decode reads exactly one Request of at most maxBody bytes.
func decode(w http.ResponseWriter, r *http.Request) (Request, error) {
	var req Request
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			return Request{}, errTooLarge
		}
		return Request{}, errMalformed
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return Request{}, errMalformed
	}
	return req, nil
}

func (s *Server) run(ctx context.Context, req *Request) (Reply, error) {
	a := s.deps.Accounts
	switch req.Command {
	case ResetPassword:
		return Reply{}, a.ResetPassword(ctx, req.Username, req.Password)
	case RecoveryLink:
		link, err := a.RecoveryLink(ctx, req.Username)
		return Reply{Secret: link}, err
	case CreateUser:
		return Reply{}, a.CreateUser(ctx, req.Username, req.Password, req.Role)
	case CreateKey:
		key, err := a.CreateKey(ctx, req.Username, req.Label, req.Scopes)
		return Reply{Secret: key}, err
	case SetupToken:
		tok, err := a.SetupToken(ctx)
		return Reply{Secret: tok}, err
	case EnablePasswordLogin:
		out, err := s.deps.Settings.EnablePasswordLogin(ctx)
		return Reply{Fields: out.Fields, AlreadyOn: out.AlreadyOn}, err
	case Store:
		if req.Repair && !req.Verify {
			return Reply{}, errRepairNeedsVerify
		}
		in, err := s.deps.Store.Inspect(ctx, statedb.InspectOptions{Verify: req.Verify, Repair: req.Repair})
		if err != nil {
			return Reply{}, &inspectError{err}
		}
		return Reply{Inspection: &in}, nil
	}
	return Reply{}, errUnknownCommand
}

// errRepairNeedsVerify is a Store request asking for Repair without Verify.
var errRepairNeedsVerify = errors.New("adminsock: repair needs verify")

// inspectError is the store's refusal of an inspection: a damaged row or no
// settings in force, which the operator acts on, so its text is answered.
type inspectError struct{ err error }

func (e *inspectError) Error() string { return e.err.Error() }
func (e *inspectError) Unwrap() error { return e.err }

var (
	errTooLarge  = errors.New("adminsock: the request is over 4 KiB")
	errMalformed = errors.New("adminsock: the request is not one JSON object of known fields")
)

// refusal is how an error is answered with its text, which names no
// secret, logged at level; any other error is logged and answered as
// internal.
type refusal struct {
	is     func(error) bool
	code   string
	status int
	level  slog.Level
}

func sentinel(target error) func(error) bool {
	return func(err error) bool { return errors.Is(err, target) }
}

func typed[E error]() func(error) bool {
	return func(err error) bool { _, ok := errors.AsType[E](err); return ok }
}

const codeUsernameInvalid = "username_invalid"

var refusals = []refusal{
	{sentinel(errTooLarge), "body_too_large", http.StatusRequestEntityTooLarge, slog.LevelInfo},
	{sentinel(errMalformed), "bad_request", http.StatusBadRequest, slog.LevelInfo},
	{sentinel(errUnknownCommand), "unknown_command", http.StatusBadRequest, slog.LevelInfo},
	{sentinel(errRepairNeedsVerify), "bad_request", http.StatusBadRequest, slog.LevelInfo},
	{sentinel(account.ErrNotFound), "not_found", http.StatusNotFound, slog.LevelInfo},
	{sentinel(access.ErrUsernameEmpty), codeUsernameInvalid, http.StatusBadRequest, slog.LevelInfo},
	{sentinel(access.ErrUsernameInvalid), codeUsernameInvalid, http.StatusBadRequest, slog.LevelInfo},
	{typed[*account.UsernameError](), codeUsernameInvalid, http.StatusBadRequest, slog.LevelInfo},
	{sentinel(account.ErrUsernameTaken), "username_taken", http.StatusConflict, slog.LevelInfo},
	{typed[*access.PasswordError](), "password_rejected", http.StatusBadRequest, slog.LevelInfo},
	{sentinel(access.ErrBreachCheckUnavailable), "breach_check_unavailable", http.StatusServiceUnavailable, slog.LevelInfo},
	{sentinel(accounts.ErrUnknownRole), "role_invalid", http.StatusBadRequest, slog.LevelInfo},
	{sentinel(account.ErrUnknownScope), "unknown_scope", http.StatusBadRequest, slog.LevelInfo},
	{typed[*account.TextError](), "label_invalid", http.StatusBadRequest, slog.LevelInfo},
	{sentinel(account.ErrSetupComplete), "setup_complete", http.StatusConflict, slog.LevelInfo},
	{sentinel(account.ErrPolicyRefused), "forbidden", http.StatusForbidden, slog.LevelInfo},
	{typed[*inspectError](), "inspect_failed", http.StatusInternalServerError, slog.LevelError},
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, req *Request, err error) {
	for _, f := range refusals {
		if f.is(err) {
			s.deps.Logger.Log(r.Context(), f.level, "adminsock: command refused", "command", req.Command.String(), "username", textsafe.SanitizeSingleLine(req.Username), "code", f.code, "error", err)
			writeJSON(w, f.status, &Error{Code: f.code, Message: err.Error()})
			return
		}
	}
	if r.Context().Err() == nil {
		s.deps.Logger.Error("adminsock: command failed", "command", req.Command.String(), "error", err)
	}
	writeJSON(w, http.StatusInternalServerError, &Error{Code: "internal", Message: "the command failed; the server log names why"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
