package settings

import "github.com/cplieger/go/httpwire"

// Owner maps every sentinel Save returns to the code the API answers for it.
var Owner = &httpwire.Owner{
	Name: "settings",
	Sentinels: map[string]error{
		"ErrActivation": ErrActivation,
		"ErrClosed":     ErrClosed,
		"ErrConflict":   ErrConflict,
	},
	Codes: []httpwire.OwnerCode{
		{Err: ErrActivation, Code: "activation_failed"},
		{Err: ErrClosed, Code: "settings_closing"},
		{Err: ErrConflict, Code: "stale_version"},
	},
}
