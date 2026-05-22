package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"subflux/internal/api"
	"subflux/internal/auth"
	"subflux/internal/cliparse"
	"subflux/internal/clisearch"
	"subflux/internal/config"
	"subflux/internal/store"
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

	db, dbErr := store.Open(context.Background(), dbPath)
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
	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	defer db.Close(ctx)

	user, err := db.GetUserByUsername(ctx, username)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}
	if user == nil {
		return fmt.Errorf("user not found: %s", username)
	}

	fmt.Fprint(os.Stderr, "New password: ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		if scanErr := scanner.Err(); scanErr != nil {
			return fmt.Errorf("failed to read password: %w", scanErr)
		}
		return errors.New("failed to read password")
	}
	password := scanner.Text()

	if errLen := auth.ValidatePasswordLength(password, true); errLen != nil {
		return errLen
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	user.PasswordHash = hash
	if err := db.UpdateUser(ctx, user); err != nil {
		return fmt.Errorf("failed to update password: %w", err)
	}

	if err := db.DeleteUserSessions(ctx, user.ID, ""); err != nil {
		return fmt.Errorf("failed to invalidate sessions: %w", err)
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
	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	defer db.Close(ctx)

	user, err := db.GetUserByUsername(ctx, username)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}
	if user == nil {
		return fmt.Errorf("user not found: %s", username)
	}

	plaintext, hash, prefix, suffix, err := auth.GenerateAPIKey()
	if err != nil {
		return fmt.Errorf("failed to generate API key: %w", err)
	}

	apiKey := &api.Key{
		UserID:    user.ID,
		KeyHash:   hash,
		KeyPrefix: prefix,
		KeySuffix: suffix,
		Label:     label,
	}
	if err := db.CreateAPIKey(ctx, apiKey); err != nil {
		return fmt.Errorf("failed to store API key: %w", err)
	}

	fmt.Println(plaintext)
	return nil
}
