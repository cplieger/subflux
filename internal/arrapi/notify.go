package arrapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"subflux/internal/api"
	"subflux/internal/httputil"
)

// CommandName is a typed string for arr API command names.
type CommandName string

const (
	// CommandRescanSeries tells Sonarr to rescan a series for new files.
	CommandRescanSeries CommandName = "RescanSeries"
	// CommandRescanMovie tells Radarr to rescan a movie for new files.
	CommandRescanMovie CommandName = "RescanMovie"
)

// Valid reports whether c is a known command name.
func (c CommandName) Valid() bool {
	switch c {
	case CommandRescanSeries, CommandRescanMovie:
		return true
	}
	return false
}

// RefreshSeries tells Sonarr to rescan a series for new files (including subtitles).
func (c *Client) RefreshSeries(ctx context.Context, seriesID int) error {
	return c.postCommand(ctx, commandBody{Name: CommandRescanSeries, SeriesID: seriesID})
}

// RefreshMovie tells Radarr to rescan a movie for new files.
func (c *Client) RefreshMovie(ctx context.Context, movieID int) error {
	return c.postCommand(ctx, commandBody{Name: CommandRescanMovie, MovieID: movieID})
}

// commandBody is the typed request body for arr API commands.
type commandBody struct {
	Name     CommandName `json:"name"`
	SeriesID int         `json:"seriesId,omitempty"`
	MovieID  int         `json:"movieId,omitempty"`
}

// postCommand sends a command to the Sonarr/Radarr API and returns any error.
func (c *Client) postCommand(ctx context.Context, body commandBody) error {
	if !body.Name.Valid() {
		return fmt.Errorf("invalid command name: %q", body.Name)
	}
	if _, ok := ctx.Deadline(); !ok && c.defaultTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.defaultTimeout)
		defer cancel()
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal command %s: %w", body.Name, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+apiPrefix+"/command", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build command request %s: %w", body.Name, err)
	}
	req.Header.Set(api.HeaderXAPIKey, c.apiKey)
	req.Header.Set(httputil.HeaderContentType, httputil.ContentTypeJSON)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post command %s: %w", body.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		errBody, err := io.ReadAll(io.LimitReader(resp.Body, httputil.MaxErrorBodyBytes))
		if err != nil {
			return newStatusError(resp.StatusCode, apiPrefix+"/command/"+string(body.Name), "")
		}
		return newStatusError(resp.StatusCode, apiPrefix+"/command/"+string(body.Name), string(errBody))
	}

	// Drain body to enable HTTP connection reuse.
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, httputil.MaxErrorBodyBytes)); err != nil {
		slog.Debug("failed to drain command response", "command", body.Name, "error", err)
	}

	slog.Debug("arr command sent", "command", body.Name, "status", resp.StatusCode)
	return nil
}
