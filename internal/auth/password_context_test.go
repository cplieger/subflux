package auth

import "testing"

func TestValidatePasswordContext(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		password string
		username string
		wantErr  bool
	}{
		{"clean", "correct horse battery staple", "alice", false},
		{"contains app name", "mysubfluxpassword", "alice", true},
		{"contains app name mixed case", "MySubFluxPass", "alice", true},
		{"contains username", "alice-secret-passphrase", "alice", true},
		{"contains username mixed case", "xxALICExxpadding", "alice", true},
		{"short username ignored", "bobsyouruncle-long-pass", "bob", false},
		{"empty username ok", "a-perfectly-fine-passphrase", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidatePasswordContext(tc.password, tc.username)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidatePasswordContext(%q, %q) err = %v, wantErr = %v",
					tc.password, tc.username, err, tc.wantErr)
			}
		})
	}
}
