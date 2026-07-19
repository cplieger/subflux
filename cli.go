package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cplieger/atomicfile/v2"
	"github.com/cplieger/subflux/internal/cliparse"
	"github.com/cplieger/subflux/internal/config"
	"go.yaml.in/yaml/v3"
	"golang.org/x/term"
)

// --- CLI Auth Commands ---

// adminSocketRequest posts a JSON body to the admin bootstrap endpoint over
// the Unix-socket admin plane (config.AdminSocketPath). These commands are
// same-container by construction (the socket is only reachable from inside
// the container as the server's UID), so SUBFLUX_URL does not participate:
// the transport dials the socket directly and the URL host is a placeholder.
// The request context is cancelled on SIGINT/SIGTERM so Ctrl+C aborts
// cleanly.
func adminSocketRequest(body []byte) (data []byte, status int, err error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", config.AdminSocketPath)
			},
		},
	}
	defer client.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://admin.sock"+config.AdminBootstrapURLPath, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf(
			"server admin socket unreachable at %s — is the server running in this container? (%w)",
			config.AdminSocketPath, err)
	}
	defer resp.Body.Close()
	data, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, 0, fmt.Errorf("read response: %w", err)
	}
	return data, resp.StatusCode, nil
}

// runCLIResetPassword resets a user's password via stdin.
// Usage: subflux reset-password --user <username>
// Returns 0 on success, 1 on runtime failure.
func runCLIResetPassword(p cliparse.Params) int {
	if err := doResetPassword(p.String("user")); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	return 0
}

// maxPasswordLen bounds the password read from stdin, in bytes. The server
// rejects passwords over 128 characters (auth.PasswordMaxLength), so 1 KiB
// comfortably covers every acceptable password while keeping the read
// bounded. Error paths report only lengths, never the password itself.
const maxPasswordLen = 1024

// readPassword reads the new password from f. On an interactive terminal
// it reads with echo disabled — the password must not appear on screen or
// in terminal recordings — and prints the newline the suppressed echo
// swallowed. Non-TTY input (deliberate piping/automation) falls back to a
// bounded single-line read.
func readPassword(f *os.File) (string, error) {
	fd := int(f.Fd())
	if !term.IsTerminal(fd) {
		return readPasswordLine(f)
	}
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr) // terminate the prompt line the disabled echo left open
	if err != nil {
		return "", fmt.Errorf("failed to read password: %w", err)
	}
	if len(b) > maxPasswordLen {
		return "", fmt.Errorf("password exceeds %d bytes", maxPasswordLen)
	}
	return string(b), nil
}

// readPasswordLine is the non-TTY password read: the first line of r,
// bounded at maxPasswordLen bytes. Extracted from readPassword so the
// piped path is unit-testable (a real PTY is impractical in unit tests).
func readPasswordLine(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	// +2 admits a maxPasswordLen-byte line plus its \r\n so the explicit
	// length check below owns the boundary error.
	scanner.Buffer(make([]byte, 0, 256), maxPasswordLen+2)
	if !scanner.Scan() {
		if scanErr := scanner.Err(); scanErr != nil {
			if errors.Is(scanErr, bufio.ErrTooLong) {
				return "", fmt.Errorf("password exceeds %d bytes", maxPasswordLen)
			}
			return "", fmt.Errorf("failed to read password: %w", scanErr)
		}
		return "", errors.New("failed to read password")
	}
	if len(scanner.Bytes()) > maxPasswordLen {
		return "", fmt.Errorf("password exceeds %d bytes", maxPasswordLen)
	}
	return scanner.Text(), nil
}

func doResetPassword(username string) error {
	fmt.Fprint(os.Stderr, "New password: ")
	password, err := readPassword(os.Stdin)
	if err != nil {
		return err
	}

	body, err := json.Marshal(map[string]string{
		"action":   "reset-password",
		"username": username,
		"password": password,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	data, status, err := adminSocketRequest(body)
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("server error (%d): %s", status, string(data))
	}

	fmt.Fprintf(os.Stderr, "Password reset for %s\n", username)
	return nil
}

// runCLIGenerateAPIKey generates an API key for a user.
// Usage: subflux generate-api-key --user <username> --label <label>
// Returns 0 on success, 1 on runtime failure.
func runCLIGenerateAPIKey(p cliparse.Params) int {
	if err := doGenerateAPIKey(p.String("user"), p.String("label")); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	return 0
}

func doGenerateAPIKey(username, label string) error {
	body, err := json.Marshal(map[string]string{
		"action":   "generate-api-key",
		"username": username,
		"label":    label,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	data, status, err := adminSocketRequest(body)
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("server error (%d): %s", status, string(data))
	}

	var resp struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	fmt.Println(resp.Key)
	return nil
}

// runCLIEnablePasswordLogin re-enables password login by setting
// auth.basic_enabled: true in the config file. Lockout recovery for when
// password login was disabled and the OIDC path is unavailable.
// Returns 0 on success, 1 on runtime failure.
func runCLIEnablePasswordLogin(cliparse.Params) int {
	if err := doEnablePasswordLogin(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	return 0
}

func doEnablePasswordLogin() error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config %s: %w", configPath, err)
	}
	out, err := enablePasswordLoginYAML(data)
	if err != nil {
		return err
	}
	if _, err := atomicfile.WriteFile(context.Background(), configPath, out,
		atomicfile.WithMode(0o600)); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	fmt.Fprintln(os.Stderr, "Password login re-enabled (auth.basic_enabled: true). Restart subflux to apply.")
	return nil
}

// enablePasswordLoginYAML parses config YAML and returns it with
// auth.basic_enabled set to true, creating the auth mapping when it is
// absent and coercing it from a non-mapping value (e.g. null). Sibling
// keys and sections are preserved. This is the pure transform extracted
// from doEnablePasswordLogin so the YAML rewrite is testable without
// touching the on-disk config at the fixed container path. Returns an
// error for malformed or empty YAML.
func enablePasswordLoginYAML(data []byte) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if len(doc.Content) == 0 {
		return nil, errors.New("config is empty")
	}
	yamlSetBool(yamlChild(doc.Content[0], "auth"), "basic_enabled", true)
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	return out, nil
}

// yamlChild returns the mapping node for key under m, creating it if absent
// or coercing a non-mapping value (e.g. null) into an empty mapping.
func yamlChild(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			v := m.Content[i+1]
			if v.Kind != yaml.MappingNode {
				v.Kind, v.Tag, v.Value, v.Content = yaml.MappingNode, "", "", nil
			}
			return v
		}
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
	v := &yaml.Node{Kind: yaml.MappingNode}
	m.Content = append(m.Content, k, v)
	return v
}

// yamlSetBool sets key to a boolean value in mapping m, adding or replacing it.
func yamlSetBool(m *yaml.Node, key string, val bool) {
	s := "false"
	if val {
		s = "true"
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1].Kind, m.Content[i+1].Tag, m.Content[i+1].Value = yaml.ScalarNode, "!!bool", s
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: s})
}
