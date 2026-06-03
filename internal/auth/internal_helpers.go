package auth

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// parsePHC extracts parameters from a PHC-format Argon2id hash string.
// Kept for fuzz test coverage.
func parsePHC(encoded string) (phcParams, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return phcParams{}, errors.New("auth: invalid PHC hash format")
	}

	var version int
	if _, errV := fmt.Sscanf(parts[2], "v=%d", &version); errV != nil {
		return phcParams{}, fmt.Errorf("auth: parse version: %w", errV)
	}
	if version != argon2.Version {
		return phcParams{}, fmt.Errorf("auth: unsupported argon2 version %d", version)
	}

	var p phcParams
	if _, errP := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.iterations, &p.parallelism); errP != nil {
		return phcParams{}, fmt.Errorf("auth: parse params: %w", errP)
	}

	var err error
	p.salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return phcParams{}, fmt.Errorf("auth: decode salt: %w", err)
	}

	p.key, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return phcParams{}, fmt.Errorf("auth: decode key: %w", err)
	}

	p.keyLen = uint32(len(p.key)) //nolint:gosec // G115
	return p, nil
}

// phcParams holds the parsed parameters from a PHC-format Argon2id hash string.
type phcParams struct {
	salt, key          []byte
	memory, iterations uint32
	parallelism        uint8
	keyLen             uint32
}

// formatAAGUID formats a 16-byte AAGUID as a UUID string (8-4-4-4-12).
func formatAAGUID(aaguid []byte) string {
	if len(aaguid) != 16 {
		return ""
	}
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		aaguid[0:4], aaguid[4:6], aaguid[6:8], aaguid[8:10], aaguid[10:16])
}
