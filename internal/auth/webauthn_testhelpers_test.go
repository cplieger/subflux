package auth

import (
	"encoding/hex"
	"strings"
)

// parseAAGUID parses a UUID string (8-4-4-4-12) into 16 bytes. Used only by
// tests that want to construct AAGUID byte slices from human-readable UUIDs.
// Production code works directly with the 16-byte AAGUID from WebAuthn
// credentials and never parses UUID strings.
func parseAAGUID(s string) []byte {
	clean := strings.ReplaceAll(s, "-", "")
	b, err := hex.DecodeString(clean)
	if err != nil || len(b) != 16 {
		return nil
	}
	return b
}
