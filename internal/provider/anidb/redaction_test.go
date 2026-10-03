package anidb

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/testsupport"
)

// echoKey holds a space so the upstream text can spell it in a form the
// single-line normalization turns back into the key.
const echoKey = "placeholder client"

const echoedRefusal = `<error code="302">unknown client placeholder client or placeholder` + "\n" + `client</error>`

// Not parallel: it swaps slog's default logger.
func TestEpisodeID_an_echoed_client_key_reaches_no_error_or_log(t *testing.T) {
	var logs bytes.Buffer
	testsupport.SwapDefaultLogger(t, slog.New(slog.NewTextHandler(&logs, nil)))
	tests := []struct {
		body         string
		transportErr error
	}{
		{body: echoedRefusal},
		{body: `<error>banned placeholder client</error>`},
		{transportErr: errors.New("connection reset")},
	}
	for _, tt := range tests {
		m := credMapper(echoKey, http.StatusOK, tt.body, tt.transportErr)
		_, err := m.episodeID(t.Context(), 1, 1)
		if err == nil {
			t.Fatalf("episodeID() with %q, %v = nil, want an error", tt.body, tt.transportErr)
		}
		if strings.Contains(err.Error(), "placeholder") {
			t.Errorf("episodeID() with %q, %v = %q, carries the client key", tt.body, tt.transportErr, err)
		}
	}
	if strings.Contains(logs.String(), "placeholder") {
		t.Errorf("log carries the client key:\n%s", logs.String())
	}
}

func TestCheckClientKey_an_echoed_client_key_reaches_no_error(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		body         string
		transportErr error
	}{
		{name: "refusal", body: echoedRefusal},
		{name: "other_error", body: `<error>banned placeholder client</error>`},
		{name: "transport", transportErr: errors.New("connection reset")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := credMapper(echoKey, http.StatusOK, tt.body, tt.transportErr)
			err := m.CheckClientKey(t.Context())
			if err == nil {
				t.Fatal("CheckClientKey() = nil, want an error")
			}
			if strings.Contains(err.Error(), "placeholder") {
				t.Errorf("CheckClientKey() = %q, carries the client key", err)
			}
		})
	}
}
