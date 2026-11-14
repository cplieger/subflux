package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/cplieger/go/textsafe"
	"github.com/cplieger/subflux/internal/adminsock"
)

// commandUsage is each local command's argument line.
var commandUsage = map[adminsock.Command]string{
	adminsock.ResetPassword:       "<user>",
	adminsock.RecoveryLink:        "<user>",
	adminsock.CreateUser:          "<user> [--role admin|user]",
	adminsock.CreateKey:           "<user> [--label <text>] [--scope <scope>]...",
	adminsock.SetupToken:          "",
	adminsock.EnablePasswordLogin: "",
	adminsock.Store:               "[--verify [--repair]]",
}

// errUsage is a command line its parser refused; the parser printed why.
var errUsage = errors.New("usage")

// command runs the local command args names through the admin socket and
// answers its exit code, and false when args[0] is no command's word.
func command(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, bool) {
	var c adminsock.Command
	if c.UnmarshalText([]byte(args[0])) != nil {
		return 0, false
	}
	req, err := parse(c, args[1:], stdin, stderr)
	switch {
	case errors.Is(err, errUsage):
		_, _ = fmt.Fprintln(stderr, strings.TrimSpace("usage: subflux "+c.String()+" "+commandUsage[c]))
		return 2, true
	case err != nil:
		return fail(stderr, err), true
	}
	reply, err := adminsock.Call(ctx, paths.Socket, req)
	if err != nil {
		return fail(stderr, err), true
	}
	return report(c, req, &reply, stdout, stderr), true
}

// parse reads c's arguments into a Request, with the password from stdin
// for the commands that set one.
func parse(c adminsock.Command, args []string, stdin io.Reader, stderr io.Writer) (*adminsock.Request, error) {
	req := &adminsock.Request{Command: c}
	if strings.HasPrefix(commandUsage[c], "<user>") {
		if len(args) == 0 || strings.HasPrefix(args[0], "-") {
			return nil, errUsage
		}
		req.Username, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet(c.String(), flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {}
	switch c {
	case adminsock.CreateUser:
		fs.StringVar(&req.Role, "role", "user", "admin or user")
	case adminsock.CreateKey:
		fs.StringVar(&req.Label, "label", "", "the key's label")
		fs.Func("scope", "a scope the key carries; none is full access", func(s string) error {
			req.Scopes = append(req.Scopes, s)
			return nil
		})
	case adminsock.Store:
		fs.BoolVar(&req.Verify, "verify", false, "rescan every index, counter and derived row")
		fs.BoolVar(&req.Repair, "repair", false, "with --verify, rewrite what differs")
	}
	if fs.Parse(args) != nil || fs.NArg() != 0 || (req.Repair && !req.Verify) {
		return nil, errUsage
	}
	if c == adminsock.ResetPassword || c == adminsock.CreateUser {
		pw, err := password(stdin, stderr)
		if err != nil {
			return nil, err
		}
		req.Password = pw
	}
	return req, nil
}

// report writes c's answer and returns the exit code.
func report(c adminsock.Command, req *adminsock.Request, r *adminsock.Reply, stdout, stderr io.Writer) int {
	user := textsafe.SanitizeSingleLine(req.Username)
	switch c {
	case adminsock.ResetPassword:
		_, _ = fmt.Fprintf(stdout, "reset the password of %s; their sessions and keys are revoked\n", user)
	case adminsock.CreateUser:
		_, _ = fmt.Fprintf(stdout, "created %s\n", user)
	case adminsock.EnablePasswordLogin:
		switch {
		case r.AlreadyOn:
			_, _ = fmt.Fprintln(stdout, "password sign-in is already on: there is no config.yaml; nothing was written")
		case len(r.Fields) > 0:
			_, _ = fmt.Fprintln(stderr, "config.yaml was not changed:")
			for _, f := range r.Fields {
				_, _ = fmt.Fprintln(stderr, "  "+textsafe.SanitizeSingleLine(f.Path+": "+f.Message))
			}
			return 1
		default:
			_, _ = fmt.Fprintln(stdout, "password sign-in is on")
		}
	case adminsock.Store:
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(r.Inspection)
	default:
		_, _ = fmt.Fprintln(stdout, r.Secret)
	}
	return 0
}

// fail prints err as one JSON error on stderr and answers exit code 1.
func fail(stderr io.Writer, err error) int {
	e, ok := errors.AsType[*adminsock.Error](err)
	if !ok {
		e = &adminsock.Error{Code: "failed", Message: textsafe.SanitizeSingleLine(err.Error())}
	}
	b, _ := json.Marshal(e)
	_, _ = fmt.Fprintln(stderr, string(b))
	return 1
}
