// Package adminsock is the admin socket: the server inside the container,
// on a Unix socket whose directory custody is the only authority, and the
// client the local commands call it with.
package adminsock

import (
	"errors"
	"slices"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/statedb"
)

// Command is one admin socket action. It travels, and the local CLI parses
// it, as its CLI word.
type Command uint8

// The commands, in Words order.
const (
	ResetPassword Command = iota + 1
	RecoveryLink
	CreateUser
	CreateKey
	SetupToken
	EnablePasswordLogin
	Store
)

var words = [...]string{
	ResetPassword:       "reset-password",
	RecoveryLink:        "recovery-link",
	CreateUser:          "create-user",
	CreateKey:           "create-key",
	SetupToken:          "setup-token",
	EnablePasswordLogin: "enable-password-login",
	Store:               "store",
}

// errUnknownCommand is a Command or a word outside Words.
var errUnknownCommand = errors.New("adminsock: unknown command")

// MarshalText answers c's CLI word.
func (c Command) MarshalText() ([]byte, error) {
	if c == 0 || int(c) >= len(words) {
		return nil, errUnknownCommand
	}
	return []byte(words[c]), nil
}

// UnmarshalText reads a CLI word.
func (c *Command) UnmarshalText(b []byte) error {
	for w := ResetPassword; int(w) < len(words); w++ {
		if words[w] == string(b) {
			*c = w
			return nil
		}
	}
	return errUnknownCommand
}

// String is c's CLI word, or "" for an unknown Command.
func (c Command) String() string {
	b, _ := c.MarshalText()
	return string(b)
}

// Words lists every command's CLI word once, so a generated remote CLI can
// refuse a group that would shadow one.
func Words() []string { return slices.Clone(words[1:]) }

// Request is one command and its arguments.
type Request struct {
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"` // ResetPassword, CreateUser; never logged
	Role     string   `json:"role,omitempty"`     // CreateUser: admin or user
	Label    string   `json:"label,omitempty"`    // CreateKey
	Scopes   []string `json:"scopes,omitempty"`   // CreateKey; none is full access
	Command  Command  `json:"command"`
	Verify   bool     `json:"verify,omitempty"` // Store
	Repair   bool     `json:"repair,omitempty"` // Store, with Verify
}

// Reply is a command's answer.
type Reply struct {
	// Inspection is Store's answer.
	Inspection *statedb.Inspection `json:"inspection,omitempty"`
	// Secret is the key, link or token, shown once.
	Secret string `json:"secret,omitempty"`
	// Fields are EnablePasswordLogin's refusal: the Save's field errors, or
	// the load's when there is no document to save.
	Fields []formschema.FieldError `json:"fields,omitempty"`
	// AlreadyOn is EnablePasswordLogin with no file: password sign-in is on
	// and nothing was written.
	AlreadyOn bool `json:"already_on,omitempty"`
}
