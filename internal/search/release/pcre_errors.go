package release

import "fmt"

// CompileError is the typed error returned by CompilePCRE for malformed
// patterns and unsupported or semantically rejected constructs (spec
// subflux-release-parse-fidelity R3.6). It carries the offending construct,
// the byte offset in the SOURCE pattern (-1 when unknown, e.g. errors
// surfaced by the underlying RE2 compiler without position info), and a
// human-readable reason.
type CompileError struct {
	Construct string // offending construct, e.g. "(?=", "(?>", "\\k", "++"
	Reason    string // why it was rejected
	Offset    int    // byte offset in the source pattern, -1 when unknown
}

// Error implements the error interface.
func (e *CompileError) Error() string {
	if e.Offset >= 0 {
		return fmt.Sprintf("pcre compile: %s at offset %d: %s", e.Construct, e.Offset, e.Reason)
	}
	return fmt.Sprintf("pcre compile: %s: %s", e.Construct, e.Reason)
}

// compileErr builds a *CompileError.
func compileErr(construct string, offset int, reason string) *CompileError {
	return &CompileError{Construct: construct, Offset: offset, Reason: reason}
}
