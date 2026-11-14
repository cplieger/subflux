package formschema

import "strings"

// RefusedError is an activation's refusal of a Save on the user's values:
// settings restores the baseline and answers Fields as field errors, not
// ErrActivation.
type RefusedError struct{ Fields []FieldError }

func (r *RefusedError) Error() string {
	parts := make([]string, 0, len(r.Fields))
	for _, f := range r.Fields {
		if f.Path == "" {
			parts = append(parts, f.Message)
			continue
		}
		parts = append(parts, f.Path+": "+f.Message)
	}
	return "formschema: refused: " + strings.Join(parts, "; ")
}
