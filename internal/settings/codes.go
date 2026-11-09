package settings

// Codes is every sentinel Save returns, with the code api answers for it.
// The element type is unnamed, so settings never imports api's wirespec.
var Codes = []struct {
	Err  error
	Code string
}{
	{ErrActivation, "activation_failed"},
	{ErrClosed, "settings_closing"},
	{ErrConflict, "settings_conflict"},
}
