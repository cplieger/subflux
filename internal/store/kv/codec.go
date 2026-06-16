package boltkv

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

// Encode serialises a bucket value as JSON. Bucket values are JSON
// (stdlib-only, debuggable, and additively forward-compatible without a
// migration script); bucket keys remain binary.
func Encode(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("boltkv: encode: %w", err)
	}
	return data, nil
}

// Decode deserialises a JSON bucket value into v, returning an error on
// malformed input. Callers decide what to do with that error: a read over a
// derived core bucket skips the record with a warning (the next scan rebuilds
// it), while an auth, lock, or uniqueness read fails closed. [DecodeOrHandle]
// centralises that decision.
func Decode(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("boltkv: decode: %w", err)
	}
	return nil
}

// DecodeMode selects how a cursor-walking read handles a record it cannot
// decode.
type DecodeMode int

const (
	// FailClosed treats an undecodable record as a fatal error for the read.
	// Use it for auth buckets and for lock- or uniqueness-bearing reads, where
	// silently skipping a row could drop a security-relevant record (treat any
	// affected lock as held rather than absent).
	FailClosed DecodeMode = iota

	// TolerantSkip logs a warning and skips an undecodable record so the scan
	// continues. Use it for derived core buckets, which the next full scan
	// rebuilds.
	TolerantSkip
)

// String implements fmt.Stringer.
func (m DecodeMode) String() string {
	switch m {
	case FailClosed:
		return "fail-closed"
	case TolerantSkip:
		return "tolerant-skip"
	default:
		return fmt.Sprintf("DecodeMode(%d)", int(m))
	}
}

// DecodeOrHandle decodes data into v and applies the decode-failure policy for
// mode. On success it returns (false, nil). On a decode error it returns
// (true, nil) after logging a warning in [TolerantSkip] mode, or (false, err)
// in [FailClosed] mode. Callers walking a cursor use the skip flag to continue
// to the next record.
func DecodeOrHandle(mode DecodeMode, bucket string, key, data []byte, v any) (skip bool, err error) {
	if derr := Decode(data, v); derr != nil {
		if mode == TolerantSkip {
			slog.Warn("boltkv: skipping undecodable record",
				"bucket", bucket, "key", fmt.Sprintf("%x", key), "error", derr)
			return true, nil
		}
		return false, fmt.Errorf("boltkv: decode %s record: %w", bucket, derr)
	}
	return false, nil
}
