package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// Test seams over the terminal: whether stdin is one, and a read with echo
// off.
var (
	isTerminal = func(r io.Reader) bool {
		f, ok := r.(*os.File)
		return ok && term.IsTerminal(int(f.Fd()))
	}
	readPassword = func(r io.Reader) ([]byte, error) {
		f, ok := r.(*os.File)
		if !ok {
			return nil, errNotTerminal
		}
		return term.ReadPassword(int(f.Fd()))
	}
)

// maxPasswordBytes bounds a piped password, under the socket's 4 KiB body
// cap. pipedLineBytes reads it with its CRLF; a read that fills it with no
// newline holds more than the bound, whatever trimming would leave.
const (
	maxPasswordBytes = 1 << 10
	pipedLineBytes   = maxPasswordBytes + 2
)

var (
	errPasswordsDiffer = errors.New("the two passwords differ")
	errPasswordEmpty   = errors.New("no password given")
	errPasswordLong    = fmt.Errorf("the password is over %d bytes", maxPasswordBytes)
	errNotTerminal     = errors.New("stdin is not a terminal")
)

// password reads the new password: twice with echo off from a terminal,
// prompting on stderr, or one line from piped stdin.
func password(stdin io.Reader, stderr io.Writer) (string, error) {
	if !isTerminal(stdin) {
		line, err := bufio.NewReader(io.LimitReader(stdin, pipedLineBytes)).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read the password: %w", err)
		}
		if len(line) == pipedLineBytes && !strings.HasSuffix(line, "\n") {
			return "", errPasswordLong
		}
		switch line = strings.TrimRight(line, "\r\n"); {
		case line == "":
			return "", errPasswordEmpty
		case len(line) > maxPasswordBytes:
			return "", errPasswordLong
		}
		return line, nil
	}
	var typed [2]string
	for i, prompt := range []string{"New password: ", "Repeat it: "} {
		_, _ = fmt.Fprint(stderr, prompt)
		b, err := readPassword(stdin)
		_, _ = fmt.Fprintln(stderr)
		if err != nil {
			return "", fmt.Errorf("read the password: %w", err)
		}
		typed[i] = string(b)
	}
	switch {
	case typed[0] == "":
		return "", errPasswordEmpty
	case typed[0] != typed[1]:
		return "", errPasswordsDiffer
	}
	return typed[0], nil
}
