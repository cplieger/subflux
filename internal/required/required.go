// Package required answers whether a required collaborator handed to a
// constructor is missing.
//
// A nil pointer passed as an interface value, or a pointer field boxed into
// any for this check, is a live interface for which `v == nil` is false. A
// guard written that way passes while the bug it exists for is live, and the
// first method call panics far from the constructor.
package required

import "reflect"

// Missing reports whether v is nil, including a nil pointer, map, slice,
// func or channel held in a non-nil interface.
func Missing(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return rv.IsNil()
	default:
		return false
	}
}
