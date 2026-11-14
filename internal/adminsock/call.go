package adminsock

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
)

// maxReply bounds a reply's body; an Inspection is the largest.
const maxReply = 1 << 20

// Error is the server's refusal of a command: Code is stable, Message
// names no secret.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"error"`
}

func (e *Error) Error() string { return e.Message }

// Call sends r over the socket at path and answers the server's Reply, or
// its refusal as an *Error.
func Call(ctx context.Context, path string, r *Request) (Reply, error) {
	body, err := json.Marshal(r) //nolint:gosec // G117: the password travels to the socket by design; Server never logs it
	if err != nil {
		return Reply{}, fmt.Errorf("adminsock: %w", err)
	}
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		},
	}}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://admin-socket/", bytes.NewReader(body))
	if err != nil {
		return Reply{}, fmt.Errorf("adminsock: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return Reply{}, fmt.Errorf("adminsock: is subflux running? %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxReply))
	if resp.StatusCode != http.StatusOK {
		e := &Error{}
		if err := dec.Decode(e); err != nil || e.Code == "" {
			return Reply{}, fmt.Errorf("adminsock: the server answered %s", resp.Status)
		}
		return Reply{}, e
	}
	var reply Reply
	if err := dec.Decode(&reply); err != nil {
		return Reply{}, fmt.Errorf("adminsock: read the reply: %w", err)
	}
	return reply, nil
}
