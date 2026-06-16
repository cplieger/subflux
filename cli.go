package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/cplieger/subflux/internal/boltstore"
	"github.com/cplieger/subflux/internal/cliparse"
	"github.com/cplieger/subflux/internal/clisearch"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/fsutil"
	"go.yaml.in/yaml/v3"
)

// runCLISearch performs a manual subtitle search from the command line.
// Returns 0 on success, 1 on runtime failure (config load, search error).
func runCLISearch() int {
	setupLogging("info", "text")

	cfg, err := config.Load(context.Background(), configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return 1
	}

	db, dbErr := boltstore.Open(dbPath)
	if dbErr != nil {
		fmt.Fprintf(os.Stderr, "warning: database unavailable: %v\n", dbErr)
	}
	if db != nil {
		defer db.Close(context.Background())
	}

	if err := clisearch.RunSearch(context.Background(), os.Args[2:], clisearch.Deps{
		Cfg:      cfg,
		Registry: newProviderRegistry(),
		Store:    db,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	return 0
}

// --- CLI Auth Commands ---

// runCLIResetPassword resets a user's password via stdin.
// Usage: subflux reset-password --user <username>
// Returns 0 on success, 1 on runtime failure.
func runCLIResetPassword() int {
	params, _ := cliparse.ParseArgs(os.Args[2:])
	if err := doResetPassword(params["user"]); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	return 0
}

func doResetPassword(username string) error {
	fmt.Fprint(os.Stderr, "New password: ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		if scanErr := scanner.Err(); scanErr != nil {
			return fmt.Errorf("failed to read password: %w", scanErr)
		}
		return errors.New("failed to read password")
	}
	password := scanner.Text()

	body, err := json.Marshal(map[string]string{
		"action":   "reset-password",
		"username": username,
		"password": password,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	data, status, ok := cliRequest(http.MethodPost, "/api/admin/bootstrap", bytes.NewReader(body))
	if !ok {
		return errors.New("failed to connect to server")
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
func runCLIGenerateAPIKey() int {
	params, _ := cliparse.ParseArgs(os.Args[2:])
	if err := doGenerateAPIKey(params["user"], params["label"]); err != nil {
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

	data, status, ok := cliRequest(http.MethodPost, "/api/admin/bootstrap", bytes.NewReader(body))
	if !ok {
		return errors.New("failed to connect to server")
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
func runCLIEnablePasswordLogin() int {
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
	var doc yaml.Node
	if uerr := yaml.Unmarshal(data, &doc); uerr != nil {
		return fmt.Errorf("parse config: %w", uerr)
	}
	if len(doc.Content) == 0 {
		return errors.New("config is empty")
	}
	yamlSetBool(yamlChild(doc.Content[0], "auth"), "basic_enabled", true)
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := fsutil.AtomicWriteFileMode(context.Background(), configPath, out, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	fmt.Fprintln(os.Stderr, "Password login re-enabled (auth.basic_enabled: true). Restart subflux to apply.")
	return nil
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
