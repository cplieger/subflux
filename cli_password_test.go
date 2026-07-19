package main

// Tests for the reset-password stdin read (SEC-8SPEC-002): the non-TTY
// line read is extracted into readPasswordLine so its bounds and error
// paths are unit-testable, and readPassword's non-TTY dispatch is covered
// via an os.Pipe. The interactive branch (term.ReadPassword with echo
// disabled) needs a real PTY, which is impractical here; it is exercised
// manually and kept minimal by construction.

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestReadPasswordLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "plain line", input: "s3cret-passphrase\n", want: "s3cret-passphrase"},
		{name: "crlf stripped", input: "s3cret-passphrase\r\n", want: "s3cret-passphrase"},
		{name: "no trailing newline", input: "s3cret-passphrase", want: "s3cret-passphrase"},
		{name: "only first line read", input: "first\nsecond\n", want: "first"},
		{name: "empty line is accepted (server rejects it)", input: "\n", want: ""},
		{name: "empty input errors", input: "", wantErr: true},
		{name: "exactly max length accepted", input: strings.Repeat("a", maxPasswordLen) + "\n", want: strings.Repeat("a", maxPasswordLen)},
		{name: "one over max rejected", input: strings.Repeat("a", maxPasswordLen+1) + "\n", wantErr: true},
		{name: "far over max rejected (ErrTooLong path)", input: strings.Repeat("a", 4*maxPasswordLen) + "\n", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := readPasswordLine(strings.NewReader(c.input))
			if (err != nil) != c.wantErr {
				t.Fatalf("readPasswordLine() error = %v, wantErr %v", err, c.wantErr)
			}
			if err != nil {
				// The password must never leak into the error text.
				if len(c.input) > 0 && strings.Contains(err.Error(), strings.TrimRight(c.input, "\n")[:min(16, len(c.input))]) {
					t.Errorf("error text %q leaks password content", err.Error())
				}
				return
			}
			if got != c.want {
				t.Errorf("readPasswordLine() = %q, want %q", got, c.want)
			}
		})
	}
}

// A non-EOF reader error must propagate wrapped, not as ErrTooLong. (The
// reader fails on its first Read: with buffered data bufio.Scanner yields
// that data as a final token and only surfaces the error afterwards.)
func TestReadPasswordLine_reader_error_propagates(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("boom")
	_, err := readPasswordLine(&failingReader{err: wantErr})
	if !errors.Is(err, wantErr) {
		t.Errorf("readPasswordLine(failing reader) error = %v, want wrapped %v", err, wantErr)
	}
}

type failingReader struct{ err error }

func (f *failingReader) Read([]byte) (int, error) { return 0, f.err }

// readPassword on a non-TTY *os.File (a pipe, as in `echo pw | subflux
// reset-password`) must take the bounded line-read path.
func TestReadPassword_non_tty_uses_line_read(t *testing.T) {
	t.Parallel()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	go func() {
		_, _ = w.Write([]byte("piped-passphrase\n"))
		w.Close()
	}()
	got, err := readPassword(r)
	if err != nil {
		t.Fatalf("readPassword(pipe) error = %v, want nil", err)
	}
	if got != "piped-passphrase" {
		t.Errorf("readPassword(pipe) = %q, want %q", got, "piped-passphrase")
	}
}

// An over-long piped password is rejected with a length-only error.
func TestReadPassword_non_tty_bounds_input(t *testing.T) {
	t.Parallel()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	go func() {
		_, _ = w.Write([]byte(strings.Repeat("x", 8*maxPasswordLen)))
		w.Close()
	}()
	_, err = readPassword(r)
	if err == nil {
		t.Fatal("readPassword(oversized pipe) = nil error, want length error")
	}
	if strings.Contains(err.Error(), "xxx") {
		t.Errorf("error text %q leaks password content", err.Error())
	}
}
