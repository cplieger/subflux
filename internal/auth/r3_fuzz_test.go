package auth

import (
	"testing"

	"subflux/internal/api"
)

func FuzzCanDisableAuthMethod(f *testing.F) {
	f.Add("password", true, 1, true, true)
	f.Add("passkey", false, 0, false, false)
	f.Add("oidc", true, 2, true, true)
	f.Add("password", false, 0, false, false)
	f.Add("unknown", true, 1, true, true)

	f.Fuzz(func(t *testing.T, method string, hasPassword bool, passkeyCount int, oidcEnabled, oidcLinked bool) {
		if passkeyCount < 0 {
			passkeyCount = 0
		}
		if passkeyCount > 10 {
			passkeyCount = 10
		}

		m := api.AuthMethod(method)
		result := CanDisableAuthMethod(m, hasPassword, passkeyCount, oidcEnabled, oidcLinked)

		// Manually compute expected: count remaining methods after removing `method`
		remaining := 0
		if m != api.MethodPassword && hasPassword {
			remaining++
		}
		if m != api.MethodPasskey && passkeyCount > 0 {
			remaining++
		}
		if m != api.MethodOIDC && oidcEnabled && oidcLinked {
			remaining++
		}
		want := remaining > 0

		if result != want {
			t.Errorf("CanDisableAuthMethod(%q, %v, %d, %v, %v) = %v, want %v",
				method, hasPassword, passkeyCount, oidcEnabled, oidcLinked, result, want)
		}

		// Invariant: if all methods false/zero, can never disable any
		if !hasPassword && passkeyCount == 0 && (!oidcEnabled || !oidcLinked) {
			if result {
				t.Errorf("should not allow disable when no methods available")
			}
		}
	})
}
